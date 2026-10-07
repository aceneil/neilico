# NEILICO 内核接入详细计划（方案②进程隔离）

本文件是 `rust-core/README.md` 的展开：把「以后要做的每一步」写清楚，
使得接内核的人不需要再做架构决策。

## 1. 合规边界（先读这一节）

- RustDesk 主仓库为 **AGPL-3.0**。我们的合规模型是「**独立程序 + 进程边界**」：
  - 内核以**独立可执行文件**形式编译与分发；
  - NEILICO 客户端**不链接**其任何源码/静态库；
  - 二者之间只通过操作系统层面的进程接口（命令行、配置文件、文件/网络）交互。
- 因此：
  - **禁止**在 `desktop/` 的 `pubspec.yaml` / CMake / Cargo 中出现对内核算法的依赖。
  - **禁止**在 `rust-core/` 里修改上游源码；需要改动时开独立 fork，并在 `docs/` 里记录许可评估结论。
  - 对外分发时，内核的源码获取方式与许可文本需随安装包提供（AGPL 的对应源码义务落在此处）。

> 若后续法务要求更严格（例如内核与客户端被认定为「同一程序」），
> 退路是把内核作为**用户自行安装的第三方软件**（我们只检测与调用），
> 即 `KernelLocator` 保持「找不到就用系统已安装的 rustdesk」这条分支。

## 2. 构建

### 2.1 依赖（三端差异很大，故本轮不执行）

| 平台 | 主要依赖 |
| --- | --- |
| Linux | `clang` `cmake` `ninja-build` `pkg-config` `libgtk-3-dev` `libxdo-dev` `libssl-dev` `libxcb`+`libxfixes`+`libxrandr`（X11 相关） `vcpkg`（FFmpeg 等） |
| Windows | Visual Studio 2022 C++ 工具链、`vcpkg`、LLVM |
| macOS | Xcode command line tools、`vcpkg` |

> 关键点：**内核构建链路与 Flutter 客户端构建链路完全独立**。
> 客户端的 `flutter build linux --release` 在缺少上述依赖时**照样成功**（已验证）。

### 2.2 产物约定

```
rust-core/dist/<os>-<arch>/neilico-kernel[.exe]
```

- 由 `scripts/fetch-kernel.sh` 产出（骨架；含可复现步骤与校验）。
- CI 里应把该产物作为 release artifact 上传，与客户端安装包一同分发。

### 2.3 可复现性

- `VERSIONS.yaml` 固定 `upstream_ref`（tag + commit + tarball SHA256）。
- 构建容器固定（推荐在容器/虚拟机里构建 Linux 产物，避免宿主漂移）。
- 产物附带 `BUILD-INFO`（commit、编译器版本、构建时间）。

## 3. 配置注入（客户端 → 内核）

`KernelLaunchConfig.toToml()` 生成的最小配置：

```toml
[server]
id_server    = "rd.neilico.local"
relay_server = "rd.neilico.local"
public_key   = "<base64 公钥>"

[render]
kernel_only = true

[identity]           # 可选
device_id = "<期望的 RustDesk ID>"
```

安全约定：

- 只写 `public_key`；**任何情况下**不写私钥。
- 配置文件写在权限 `0700` 的临时目录，文件名带随机串，进程退出后删除。
- 内核自己的身份文件（`id_ed25519` / `id_ed25519.pub`）由内核生成与持有，
  目录权限 `0700`、私钥 `0600`；NEILICO 只允许**读取公钥**用于展示/复制。

## 4. 启动 / 停止 / 观测

1. **启动**：`neilico-kernel --config <tmp>/kernel.toml --service`
2. **就绪判定**：内核日志出现「registered / listening」后再允许用户发起连接；
   在此之前 UI 显示「内核启动中」。**不做盲目等待 + 不假装成功**。
3. **停止**：`SIGTERM` → 5s → `SIGKILL`。
4. **观测**：内核 stdout/stderr 落 `~/.local/state/neilico/kernel.log`；
   NEILICO 只读日志尾部做展示，不解析内部状态。

## 5. 客户端侧的对接点（已实现，等内核就绪即可启用）

| 位置 | 作用 |
| --- | --- |
| `lib/services/kernel/kernel_locator.dart` | 按约定找可执行文件，找不到给出原因与搜索路径 |
| `lib/services/kernel/kernel_process.dart` | `buildArguments()` / `start()` / `stop()`，配置渲染 `toToml()` |
| `lib/services/connect_launcher.dart` | 内核未接入前的临时入口：调起系统已安装的 `rustdesk://<id>` |
| `lib/screens/device_detail_screen.dart` | 「内核进程（rust-core）」卡片：展示就绪状态与服务器参数 |

切换顺序（内核具备后）：

1. 先在「内核进程」卡片里把状态从「未安装」变为「已找到」；
2. 再把连接入口从 `rustdesk://` 调起改为「启动内核进程 + 传入会话参数」；
3. 保留 `rustdesk://` 作为内核缺失时的兜底。

## 6. 里程碑与验收

| 里程碑 | 验收标准 |
| --- | --- |
| M1 产物约定落地 | `rust-core/dist/<triple>/neilico-kernel` 存在；客户端「内核进程」显示「已找到」 |
| M2 进程可启停 | 客户端能拉起内核、进程列表可见、退出后 `SIGTERM` 生效、无僵尸进程 |
| M3 参数注入正确 | 内核日志显示使用控制面下发的 id/relay + 公钥完成注册 |
| M4 端到端连接 | 两台设备通过 NEILICO 建立远控会话；直连失败时回落 hbbr 中继 |
| M5 打包分发 | 安装包内含内核 + 对应源码获取说明；AGPL 文本随包 |

## 7. 风险与对策

| 风险 | 对策 |
| --- | --- |
| 内核构建耗时/易失败 | 内核与客户端构建解耦；CI 单独流水线；产物以 artifact 传递 |
| AGPL 边界被质疑 | 保持「独立程序」形态；不分发链接产物；随包提供对应源码获取方式 |
| 内核崩溃影响体验 | 客户端不崩溃；显示「内核已退出」并支持重启 |
| 版本漂移导致连不上 | `VERSIONS.yaml` 固定内核与 hbbs/hbbr 同代；升级走冒烟脚本 |
| 公钥被误当私钥处理 | 代码层只暴露公钥字段；`readPublicKeyFile` 只读 `.pub`；文档与评审检查项固化 |
