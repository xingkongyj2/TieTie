# 贴贴双人 IP 原始素材

素材原样拆分自「我的」页面个人空间卡片中的 `SpaceBuddies`。人物轮廓、渐变、五官、倾斜和两人的相对位置均保留；没有重绘或另设形象。所有 SVG 都是透明背景，可以离线打开、无损缩放。

| 文件 | 内容 | viewBox | 在原组合中的位置 |
| --- | --- | --- | --- |
| `frontend/public/ip/space-buddies-v1/blue-buddy.svg` | 前方深蓝角色，白色五官，无装饰 | `24 49 110 106` | `x=24, y=49, width=110, height=106` |
| `frontend/public/ip/space-buddies-v1/ice-buddy.svg` | 后方浅蓝角色，深蓝五官，无装饰 | `80 13 117 132` | `x=80, y=13, width=117, height=132` |
| `frontend/public/ip/space-buddies-v1/duo.svg` | 原来的双人组合，保留轨道、星点 | `0 0 210 170` | 原始完整画布 |

独立人物保留原画布坐标，通过 viewBox 去除多余空间，每侧约留 5–6 个单位。组合时，先放浅蓝角色，再放深蓝角色，使用上表的 `x / y / width / height`，即可还原原画中人物的位置和遮挡关系。

配色：深蓝人物为 `#78CCFF → #3779F5`，五官为 `#FFFFFF`；浅蓝人物为 `#ECF8FF → #A6D6FF`，五官为 `#38699C`。轨道为 `#B7D3F2`，星点为 `#92C9FF`、`#87B9F4`、`#B8DBFF`。

这些素材保留静态成稿；页面原有入场动画和投影属于组件样式。SVG 本身没有背景、投影、动画或外部依赖。三个文件分别使用独立且固定的渐变 ID，便于引用。

## PNG 和素材包

每个 SVG 同时提供一个同名透明 PNG，尺寸均为 1024 × 1024。人物等比居中，四周保留透明空间，可用于头像、启动页和宣传素材。`duo.png` 是保留装饰的双人组合。PNG 直接从 SVG 渲染，未重新绘制人物。

- `preview.png`：双角色预览，预览的浅色背景不包含在独立素材中。
- `space-buddies-v1.zip`：两个独立角色与组合的 SVG / PNG、预览及本文档。
- SVG / PNG 原文件位于 `frontend/public/ip/space-buddies-v1/`。

## 页面复用

`frontend/src/components/SpaceBuddies.tsx` 提供两个组件；「我的」页面继续使用原来的组合与入场动画，人物通过 `<image>` 引用上面的独立 SVG。

```tsx
import { SpaceBuddy, SpaceBuddies } from './components/SpaceBuddies'

<SpaceBuddy variant="blue" alt="深蓝小伙伴" className="my-character" />
<SpaceBuddy variant="ice" alt="浅蓝小伙伴" className="my-character" />
<SpaceBuddies className="my-character-pair" />
```

用 CSS 设置角色宽度和 `height: auto` 即可等比缩放。装饰性使用可以将 `alt` 留空；双人组合组件默认作为装饰隐藏于读屏软件。`blue`、`ice` 只是素材标识，目前没有给角色设定正式名称。
