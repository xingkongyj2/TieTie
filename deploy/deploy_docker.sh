#!/usr/bin/env bash
set -euo pipefail

if command -v docker >/dev/null 2>&1; then
  echo "Docker 已安装。"
  docker version
  exit 0
fi

if ! command -v dnf >/dev/null 2>&1; then
  echo "此脚本适用于使用 dnf 的服务器；请先按系统官方文档安装 Docker。" >&2
  exit 1
fi

dnf install -y dnf-plugins-core
dnf config-manager --add-repo https://mirrors.aliyun.com/docker-ce/linux/centos/docker-ce.repo
dnf makecache
dnf install -y docker-ce docker-ce-cli containerd.io
systemctl enable --now docker
docker version
