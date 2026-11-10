# 发布、安装包与自动更新

ZeoKeel 的安装包和更新链路全部由仓库自己的 Go 工具链完成：`mygo.json` +
`go tool mygo build` 产出安装包，应用内更新直接读取本仓库的 GitHub Release。
本文是维护者的操作说明；实现细节见
[mygo distribution](https://github.com/egoist/mygo)（`docs/distribution.md`、
`docs/updates.md`）。

## 产物

| 平台 | 发布资产 | 说明 |
| --- | --- | --- |
| Windows | `zeokeel-setup-<version>-amd64.exe`、`zeokeel-setup-<version>-arm64.exe` | NSIS 安装器：当前用户安装到 `%LOCALAPPDATA%\Programs\ZeoKeel`，无需管理员；开始菜单、卸载项、可选桌面快捷方式；`/S` 静默安装。exe 为 GUI 子系统（无命令行窗口）、带图标和版本信息 |
| Linux | `zeokeel-<version>-linux-amd64.AppImage`、`zeokeel-<version>-linux-arm64.AppImage` | 唯一的 Linux 安装包；可执行、双击运行 |
| macOS | `zeokeel-<version>.dmg` | universal（arm64 + amd64）`.app` 的磁盘镜像；Developer ID 签名 + 公证 + staple |
| 更新用 | `zeokeel-<version>-windows-<arch>.tar.gz`、`zeokeel-<version>-darwin-universal.tar.gz`、Linux 的 `.AppImage` | 应用内更新下载的归档，不是给用户手动安装的入口 |
| 校验 | `SHA256SUMS.txt` | 发布任务统一生成，更新器用它校验下载 |

`mygo build` 还会在 Linux 输出 `.deb`、`.tar.gz`、`PKGBUILD` 等中间产物，
但这些**不是**发布资产：Linux 只发布 AppImage（用户明确要求）。

## 版本与发布

- 版本唯一来源是 `VERSION`。`scripts/build-app.sh` 在构建前把版本同步进
  `mygo.json`，并通过 `MYGO_LDFLAGS` 把 `internal/host.Version= v<版本>` 链接进二进制。
- 发布前确认 `CHANGELOG.md` 有对应版本段落。
- 打标签即发布，CI 全自动，无需手工上传：

```sh
git tag v0.2.3
git push origin v0.2.3
```

`.github/workflows/release.yml` 依次：

1. 校验 tag 与 `VERSION` 一致；
2. 创建 draft release；
3. 三个平台并行构建：macOS（`macos-15`，签名/公证/dmg）、Windows
   （`ubuntu-24.04` + NSIS）、Linux（`ubuntu-24.04` + AppImage 工具链）；
4. 各平台把安装包和更新归档上传到 draft；
5. 汇总所有资产生成 `SHA256SUMS.txt`；
6. 自动把 draft 发布为正式 release（`--draft=false`）。

缺失任一平台产物则不会发布。

### 仓库 Secrets

macOS 签名/公证凭据只存在于 GitHub Secrets，仓库与提交里不出现任何密钥值：

| Secret | 内容 |
| --- | --- |
| `MACOS_CERT_P12` | Developer ID Application 证书 + 私钥的 `.p12`（base64） |
| `MACOS_CERT_PASSWORD` | 上述 `.p12` 的导出密码 |
| `APPLE_API_KEY_P8` | App Store Connect API Key（`.p8`，base64） |
| `APPLE_API_KEY_ID` | API Key 的 Key ID |
| `APPLE_API_ISSUER` | API Key 的 Issuer ID |

工作流把证书导入临时 keychain，用 `xcrun notarytool store-credentials` 建
keychain profile，再把 keychain 路径注入本次构建的 `mygo.json`；`mygo build`
负责 `codesign`（hardened runtime + timestamp）、提交公证、staple。构建后还会
执行：

```sh
codesign --verify --deep --strict build/darwin-universal/ZeoKeel.app
spctl --assess --type execute -vv build/darwin-universal/ZeoKeel.app
xcrun stapler validate build/darwin-universal/*.dmg
hdiutil verify build/darwin-universal/*.dmg
```

Windows 代码签名未启用（没有可用证书），SmartScreen 可能提示未知发布者。

## 本地构建

依赖：

- Go 1.27（`go.mod` 已把 mygo CLI 钉在 `tool` 指令里）
- `jq`（版本同步）、`rsvg-convert`（`librsvg2-bin` / `brew install librsvg`）
- Windows 安装器：`makensis`（Debian/Ubuntu `apt install nsis`）
- AppImage：`mksquashfs`、`desktop-file-validate`、`patchelf`、`file`，以及
  `go-appimage` 的 `appimagetool` + AppImage type2 runtime（见 release.yml 的安装步骤）

常用命令：

```sh
make icons      # 只从 logo/logo.svg、logo/logo-dark.svg 派生全部图标
make package    # Linux + Windows：GUI exe、NSIS 安装器、Linux 应用目录
bash scripts/build-appimage.sh build/linux-amd64 "$(cat VERSION)" amd64
bash scripts/build-app.sh -platform darwin/universal   # 仅 macOS，签名+公证+dmg
```

`scripts/build-appimage.sh` 可用 `APPIMAGE_DRY_RUN=1` 只组装 AppDir 而不调用
appimagetool，便于在无 runtime 的环境检查布局。

## Logo 单一来源

`logo/logo.svg` 与 `logo/logo-dark.svg` 是**唯一** logo 源文件（逐字节来自官方
brand channel）。打包图标、Windows exe 图标、NSIS 安装器图标、macOS `.icns`、
Linux desktop/AppImage 图标全部由 `scripts/gen-icons.sh` 从这两个文件派生：

- `assets/icon.png`（1024×1024，构建时生成，gitignore）
- `internal/brand/mark.png`（48×48）、`mark-dark@2x.png`（96×96）

不要手工添加其他 logo/图标文件；换 logo 只改 `logo/` 下的 SVG 并重新生成。

## 应用内更新（无密钥）

更新不依赖更新签名密钥，直接读取 GitHub Release：

- 更新模块配置在 `internal/assembly/assemblies/desktop.json`：
  `{"githubRepo": "zeokeel/zeokeel"}`。
- 检查：`GET https://api.github.com/repos/zeokeel/zeokeel/releases/latest`，
  版本取 tag（去掉 `v`），release notes 取 release body，按平台选择更新归档：

  | 平台 | 优先级 |
  | --- | --- |
  | Windows | `-windows-<arch>.tar.gz`（排除 setup 安装器） |
  | Linux | `-linux-<arch>.AppImage` |
  | macOS | `-darwin-universal.tar.gz` |

- 校验：从 `SHA256SUMS.txt` 资产取对应行的 SHA-256（没有时回退 GitHub 资产的
  `digest`）；`Download` 流式下载并校验大小/SHA-256 后暂存到
  `<dataDir>/updates/`。
- 安装：设置页「更新」页下载后点「安装并在下次启动生效」，把计划写入
  `updates/pending.json`（带 `kind`）；下次启动由 host 应用：
  - `appimage`：原子替换 `$APPIMAGE` 指向的文件；
  - `archive`：解压后替换应用目录（Windows/Linux）或整个 `.app`（macOS）；
  - `binary`：替换可执行文件（旧计划 / 兼容路径）。
  替换失败会保留计划并重试，不影响启动。
- UI 入口：设置 → 更新；命令面板「软件更新」直接打开该页。

限制：

- GitHub API 未认证时限额 60 次/小时/IP，受限时更新页显示检查失败并可稍后重试。
- 由包管理器（deb/系统包）安装的副本不应自更新；本项目发布只提供 AppImage，
  AppImage 由用户放在可写目录，可自更新。
- 旧版手工解压 tar.gz/zip 的用户需要先换成安装包/AppImage，之后才会进入更新链路。
- Windows 自更新依赖归档替换；被杀软/权限拦截时会保留计划并给出错误。
