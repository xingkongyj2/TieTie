#!/usr/bin/env python3
"""Publish a managed MySQL port while retaining its named data volume/config."""

import argparse
import copy
import hashlib
import http.client
import ipaddress
import json
import os
from pathlib import Path
import shlex
import shutil
import socket
import subprocess
import sys
import time
import urllib.parse
import uuid


def run(*args, check=True, env=None):
    result = subprocess.run(args, text=True, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, env=env)
    if check and result.returncode:
        # Never include Docker inspect/configuration or credential-bearing output.
        raise RuntimeError("命令执行失败：" + args[0] + "；请检查部署权限及容器状态。")
    return result


def inspect(name):
    result = run("docker", "container", "inspect", name, check=False)
    if result.returncode:
        return None
    return json.loads(result.stdout)[0]


class DockerSocket(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__("localhost", timeout=150)
        self.socket_path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.socket_path)


class Docker:
    def __init__(self):
        endpoint = os.environ.get("DOCKER_HOST")
        if not endpoint:
            context = run("docker", "context", "inspect").stdout
            endpoint = json.loads(context)[0]["Endpoints"]["docker"]["Host"]
        if not endpoint.startswith("unix://"):
            raise RuntimeError("MySQL 端口改造仅支持本机 Unix Docker socket。")
        self.socket_path = endpoint[len("unix://"):]
        self.version = self.request("GET", "/version", versioned=False)["ApiVersion"]

    def request(self, method, path, body=None, versioned=True):
        conn = DockerSocket(self.socket_path)
        encoded = None if body is None else json.dumps(body).encode()
        headers = {} if encoded is None else {"Content-Type": "application/json"}
        try:
            conn.request(method, ("/v" + self.version if versioned else "") + path,
                         body=encoded, headers=headers)
            response = conn.getresponse()
            data = response.read()
            if response.status >= 400:
                raise RuntimeError("Docker 配置操作失败（HTTP %d），原数据卷已保留。" % response.status)
            return json.loads(data) if data else None
        finally:
            conn.close()


def bindings_overlap(left, right):
    return left in ("", "0.0.0.0", "::", "*") or right in ("", "0.0.0.0", "::", "*") or left == right


def check_port(container, old, bind_ip, port):
    wanted = str(port)
    own_bindings = []
    if old and old["State"]["Running"]:
        own_bindings = [binding for values in old["HostConfig"].get("PortBindings", {}).values()
                        for binding in (values or []) if binding["HostPort"] == wanted]
    for name in run("docker", "ps", "--format", "{{.Names}}").stdout.splitlines():
        if name == container:
            continue
        other = inspect(name)
        for values in other["HostConfig"].get("PortBindings", {}).values():
            if any(row["HostPort"] == wanted and bindings_overlap(row["HostIp"], bind_ip) for row in (values or [])):
                raise RuntimeError("MySQL 发布端口 %d 已被其他容器占用。" % port)
    if not shutil.which("ss"):
        raise RuntimeError("端口预检需要 ss（iproute2）。")
    for line in run("ss", "-H", "-ltn", "sport = :%d" % port).stdout.splitlines():
        parts = line.split()
        if len(parts) < 5:
            continue
        address = parts[3].rsplit(":", 1)[0].strip("[]")
        if bindings_overlap(address, bind_ip) and not any(bindings_overlap(address, row["HostIp"]) for row in own_bindings):
            raise RuntimeError("MySQL 发布端口 %d 已被宿主机服务占用。" % port)


def firewall_rules():
    return [shlex.split(line) for line in run("iptables", "-w", "-S", "DOCKER-USER").stdout.splitlines()
            if line.startswith("-A ")]


def remove_owned_firewall(tag, keep=None):
    for rule in firewall_rules():
        if "--comment" not in rule or rule[rule.index("--comment") + 1] != tag:
            continue
        chain = rule[rule.index("-j") + 1]
        if keep and chain in keep:
            continue
        run("iptables", "-w", "-D", "DOCKER-USER", *rule[2:])
        run("iptables", "-w", "-F", chain)
        run("iptables", "-w", "-X", chain)


def install_firewall(container, port, cidrs, interface):
    tag = "tietie-mysql:" + container
    chain = "TTMYSQL_" + hashlib.sha256(container.encode()).hexdigest()[:8] + "_" + uuid.uuid4().hex[:6]
    run("iptables", "-w", "-N", chain)
    try:
        for cidr in cidrs:
            run("iptables", "-w", "-A", chain, "-s", cidr, "-j", "RETURN")
        run("iptables", "-w", "-A", chain, "-j", "REJECT", "--reject-with", "tcp-reset")
        # Install before the port exists and before any existing ESTABLISHED rule.
        # Match host DNAT's original port, not an address changed by provider NAT.
        # Only external ingress is filtered; Docker bridge traffic is unaffected.
        run("iptables", "-w", "-I", "DOCKER-USER", "1", "-i", interface,
            "-p", "tcp", "--dport", "3306", "-m", "conntrack", "--ctstate", "DNAT",
            "--ctdir", "ORIGINAL", "--ctorigdstport", str(port),
            "-m", "comment", "--comment", tag, "-j", chain)
    except Exception:
        run("iptables", "-w", "-F", chain, check=False)
        run("iptables", "-w", "-X", chain, check=False)
        raise
    return chain


def apply_firewall(config):
    if run("iptables", "-w", "-S", "DOCKER-USER", check=False).returncode:
        run("iptables", "-w", "-N", "DOCKER-USER")
    chains = {install_firewall(config["container"], port, config["cidrs"], config["interface"])
              for port in config["ports"]}
    remove_owned_firewall("tietie-mysql:" + config["container"], keep=chains)
    print("MySQL 入站白名单已安装（%s，宿主端口 %s）；已有连接同样受限制。" %
          (config["interface"], ",".join(map(str, config["ports"]))))


def firewall_paths(container):
    suffix = hashlib.sha256(container.encode()).hexdigest()[:12]
    unit = "tietie-mysql-firewall-" + suffix + ".service"
    return (unit, Path("/etc/tietie") / ("mysql-firewall-" + suffix + ".json"),
            Path("/etc/systemd/system") / unit,
            Path("/etc/systemd/system/docker.service.d") / ("tietie-mysql-" + suffix + ".conf"))


def write_file(path, content, mode=0o644):
    if path.is_file() and path.read_text() == content:
        return
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + ".tmp")
    temporary.write_text(content)
    temporary.chmod(mode)
    temporary.replace(path)


def persist_firewall(config):
    unit, config_path, unit_path, dropin = firewall_paths(config["container"])
    helper_path = Path("/usr/local/lib/tietie/configure_mysql.py")
    write_file(helper_path, Path(__file__).read_text(), 0o755)
    # Only network interface, published ports and CIDRs are persisted here.
    write_file(config_path, json.dumps(config, ensure_ascii=False, indent=2) + "\n")
    write_file(unit_path,
               "[Unit]\nDescription=TieTie MySQL ingress whitelist\n"
               "Wants=network-online.target\nAfter=network-online.target\nBefore=docker.service\n\n"
               "[Service]\nType=oneshot\nExecStart=/usr/bin/python3 " + str(helper_path) +
               " --firewall-config " + str(config_path) + "\nRemainAfterExit=yes\n\n"
               "[Install]\nWantedBy=multi-user.target\n")
    write_file(dropin, "[Unit]\nRequires=" + unit + "\nAfter=" + unit + "\n")
    run("systemctl", "daemon-reload")
    run("systemctl", "enable", unit)
    # Do not restart Docker or unrelated running containers.


def remove_persistent_firewall(container):
    unit, config_path, unit_path, dropin = firewall_paths(container)
    if unit_path.exists() or dropin.exists():
        run("systemctl", "disable", unit)
        for path in (dropin, unit_path, config_path):
            path.unlink(missing_ok=True)
        run("systemctl", "daemon-reload")


def root_ready(container, password, attempts=60):
    env = dict(os.environ, MYSQL_PWD=password)
    for _ in range(attempts):
        # An actual SQL query verifies credentials; mysqladmin ping alone also
        # returns success on Access denied. The password stays out of argv/logs.
        if run("docker", "exec", "-e", "MYSQL_PWD", container, "mysql", "-uroot",
               "-h", "127.0.0.1", "-NBe", "SELECT 1", check=False, env=env).returncode == 0:
            return
        if attempts > 1:
            time.sleep(2)
    raise RuntimeError("MySQL 未通过 root SQL 就绪检查；密码及数据卷不会被修改。")


def create_body(old, root_password, network, image, volume, bind_ip, port, container):
    if old:
        body = copy.deepcopy(old["Config"])
        body["Image"] = old["Image"]  # Preserve the actual image, not a mutable tag.
        host = copy.deepcopy(old["HostConfig"])
        endpoints = {}
        for name, value in old["NetworkSettings"]["Networks"].items():
            endpoints[name] = {key: copy.deepcopy(value[key]) for key in ("IPAMConfig", "Links", "Aliases", "DriverOpts") if value.get(key)}
            if "Aliases" in endpoints[name]:
                endpoints[name]["Aliases"] = [alias for alias in endpoints[name]["Aliases"] if alias not in (old["Id"], old["Id"][:12])]
    else:
        body = {"Image": image, "Env": ["MYSQL_ROOT_PASSWORD=" + root_password],
                "Cmd": ["--character-set-server=utf8mb4", "--collation-server=utf8mb4_bin"]}
        host = {"Binds": [volume + ":/var/lib/mysql"], "NetworkMode": network,
                "RestartPolicy": {"Name": "unless-stopped"}}
        endpoints = {}
    endpoints.setdefault(network, {"Aliases": [container]})
    body.setdefault("ExposedPorts", {})["3306/tcp"] = {}
    bindings = host.get("PortBindings") or {}
    bindings["3306/tcp"] = [{"HostIp": bind_ip, "HostPort": str(port)}]
    host["PortBindings"] = bindings
    host["PublishAllPorts"] = False
    body["HostConfig"] = host
    body["NetworkingConfig"] = {"EndpointsConfig": endpoints}
    return body


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--firewall-config")
    for name in ("env-file", "network", "container", "image", "volume", "app-container"):
        parser.add_argument("--" + name)
    args = parser.parse_args()
    if args.firewall_config:
        apply_firewall(json.loads(Path(args.firewall_config).read_text()))
        return
    if not all((args.env_file, args.network, args.container, args.image, args.volume, args.app_container)):
        parser.error("部署模式需要 env-file/network/container/image/volume/app-container。")
    values = {}
    for line in Path(args.env_file).read_text().splitlines():
        if line and not line.lstrip().startswith("#") and "=" in line:
            key, value = line.split("=", 1)
            values.setdefault(key, value)

    def setting(key, default=""):
        return os.environ.get(key) or values.get(key) or default

    bind_ip = str(ipaddress.IPv4Address(setting("MYSQL_BIND_IP", "127.0.0.1")))
    port = int(setting("MYSQL_PUBLISHED_PORT", "3306"))
    if not 1 <= port <= 65535:
        raise RuntimeError("MYSQL_PUBLISHED_PORT 必须是 1～65535。")
    cidrs = [str(ipaddress.IPv4Network(value, strict=False)) for value in setting("MYSQL_ALLOWED_CIDRS").replace(",", " ").split()]
    public = not ipaddress.IPv4Address(bind_ip).is_loopback
    interface = setting("MYSQL_PUBLIC_INTERFACE")
    if public:
        if not cidrs:
            raise RuntimeError("非回环地址发布 MySQL 必须设置 MYSQL_ALLOWED_CIDRS；默认请使用 127.0.0.1。")
        if not shutil.which("iptables") or not shutil.which("ip"):
            raise RuntimeError("公开 MySQL 需要 iptables 和 iproute2，且 Docker 必须提供 DOCKER-USER 链。")
        if os.geteuid() != 0 or not shutil.which("systemctl") or not Path("/usr/bin/python3").exists():
            raise RuntimeError("公开 MySQL 需要 root、systemd 和 /usr/bin/python3，以在 Docker 启动前恢复白名单。")
        if not interface:
            routes = json.loads(run("ip", "-j", "-4", "route", "show", "default").stdout)
            devices = {route["dev"] for route in routes if "dev" in route}
            if len(devices) != 1:
                raise RuntimeError("无法唯一识别外网入口，请设置 MYSQL_PUBLIC_INTERFACE。")
            interface = devices.pop()
        if interface in ("lo", "docker0") or interface.startswith(("br-", "veth")):
            raise RuntimeError("MYSQL_PUBLIC_INTERFACE 必须是外网入口，不能是 Docker 内网或回环接口。")
        run("ip", "link", "show", "dev", interface)
        firewall_rules()  # Permission/backend check before changing containers.

    old = inspect(args.container)
    root_password = setting("MYSQL_ROOT_PASSWORD")
    if old:
        mounts = [mount for mount in old["Mounts"] if mount["Destination"] == "/var/lib/mysql"]
        if len(mounts) != 1 or mounts[0]["Type"] != "volume" or mounts[0]["Name"] != args.volume:
            raise RuntimeError("已有 MySQL 的 /var/lib/mysql 不是配置的 named volume；拒绝重建，请核对 MYSQL_DATA_VOLUME。")
        existing_env = dict(item.split("=", 1) for item in old["Config"].get("Env", []) if "=" in item)
        old_password = existing_env.get("MYSQL_ROOT_PASSWORD", "")
        if root_password and old_password and root_password != old_password:
            raise RuntimeError("MYSQL_ROOT_PASSWORD 与已有容器配置不一致；本脚本不修改 root 密码。")
        root_password = root_password or old_password
    if not root_password:
        raise RuntimeError("请独立设置 MYSQL_ROOT_PASSWORD；已有容器可沿用其 root 配置，不能使用应用 MYSQL_PASSWORD 覆盖。")
    check_port(args.container, old, bind_ip, port)
    engine = Docker()
    desired = [{"HostIp": bind_ip, "HostPort": str(port)}]
    changed = old is None or old["HostConfig"].get("PortBindings", {}).get("3306/tcp") != desired
    if old:
        run("docker", "start", args.container)
        root_ready(args.container, root_password, attempts=10)
    else:
        run("docker", "pull", args.image)
    if public:
        # During a port change protect both the prior published port and the
        # new one; a failed recreation/rollback remains protected after reboot.
        ports = {port}
        if old:
            for row in old["HostConfig"].get("PortBindings", {}).get("3306/tcp", []) or []:
                if row["HostIp"] in ("", "0.0.0.0", "::") or not ipaddress.ip_address(row["HostIp"]).is_loopback:
                    ports.add(int(row["HostPort"]))
        firewall_config = {"container": args.container, "ports": sorted(ports),
                           "cidrs": cidrs, "interface": interface}
        apply_firewall(firewall_config)
        persist_firewall(firewall_config)

    backup = None
    try:
        if changed:
            if old:
                if inspect(args.app_container):
                    run("docker", "stop", "--time", "30", args.app_container)
                backup = args.container + "_port_backup_" + uuid.uuid4().hex[:8]
                run("docker", "stop", "--time", "120", args.container)
                run("docker", "rename", args.container, backup)
            body = create_body(old, root_password, args.network, args.image,
                               args.volume, bind_ip, port, args.container)
            engine.request("POST", "/containers/create?name=" + urllib.parse.quote(args.container), body)
            run("docker", "start", args.container)
        elif args.network not in old["NetworkSettings"]["Networks"]:
            run("docker", "network", "connect", args.network, args.container)
        root_ready(args.container, root_password)
    except Exception:
        if backup and inspect(backup):
            if inspect(args.container):
                run("docker", "stop", "--time", "30", args.container, check=False)
                run("docker", "rm", args.container, check=False)  # Never remove volumes.
            run("docker", "rename", backup, args.container)
            run("docker", "start", args.container)
            print("端口改造未完成，已恢复原 MySQL 容器；应用保持停止，请检查后重新部署。", file=sys.stderr)
        raise
    if backup:
        run("docker", "rm", backup)  # No --volumes/-v: retain the original data.
    if public and firewall_config["ports"] != [port]:
        firewall_config["ports"] = [port]
        apply_firewall(firewall_config)
        persist_firewall(firewall_config)
    if not public and shutil.which("iptables"):
        check = run("iptables", "-w", "-S", "DOCKER-USER", check=False)
        if check.returncode == 0:
            remove_owned_firewall("tietie-mysql:" + args.container)
        remove_persistent_firewall(args.container)
    print("MySQL 已就绪：%s:%d → %s:3306，数据卷 %s%s。" %
          (bind_ip, port, args.container, args.volume, "，已保留原配置重建" if old and changed else ""))


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("MySQL 部署失败：" + str(error), file=sys.stderr)
        sys.exit(1)
