# desktop —— NEILICO 桌面客户端（设备管理端）

Flutter 三端桌面应用（Linux / Windows / macOS）。它不只是「看远程画面」，
而是**设备的统一管理端**：登录 NEILICO 控制面 → 设备网格 → 按设备管理
「是否可被远程 / 隧道模式 / 单独隧道 / Mesh 加入」→ 再发起远程连接。

## 立即开始

```bash
export PATH=$HOME/flutter/bin:$PATH

flutter pub get
flutter analyze
flutter test
flutter build linux --release
flutter run -d linux --dart-define=NEILICO_API_BASE=https://neilico.example.com
```

控制面地址优先级：登录页「控制面地址」输入框 > `--dart-define=NEILICO_API_BASE`
> 内置默认 `http://127.0.0.1:8080`。登录成功后地址随会话一起持久化。

## 系统依赖

| 平台 | 依赖 |
| --- | --- |
| Linux | `clang` `cmake` `ninja-build` `pkg-config` `libgtk-3-dev`，以及 `libsecret-1-dev`（令牌安全存储用） |
| Windows | Visual Studio C++ 工具链（含 Desktop development with C++） |
| macOS | Xcode command line tools |

### ⚠️ Linux 上 `libsecret-1-dev` 的两个坑

1. **缺它无法编译**：`flutter_secure_storage_linux` 用 `pkg_check_modules(libsecret-1)`
   做硬依赖，缺少时 `flutter build linux` 直接失败。
   ```bash
   sudo apt-get install -y libsecret-1-dev
   ```
   若拿不到 root（本仓库开发机上就遇到过），可以**只解出 dev 头文件与 .pc**、
   不动系统包（运行时 `libsecret-1.so.0` 通常已存在于 `/usr/lib/x86_64-linux-gnu`）：
   ```bash
   mkdir -p /tmp/ls && cd /tmp/ls
   apt-get download libsecret-1-dev libsecret-1-0      # 下载不需要 root
   PREFIX=$HOME/.local/neilico-deps/libsecret
   mkdir -p "$PREFIX" "$HOME/.local/neilico-deps/pkgconfig"
   dpkg-deb -x libsecret-1-dev_*.deb "$PREFIX"
   dpkg-deb -x libsecret-1-0_*.deb   "$PREFIX"

   cat > "$HOME/.local/neilico-deps/pkgconfig/libsecret-1.pc" <<PC
   prefix=$PREFIX/usr
   includedir=\${prefix}/include
   Name: libsecret-1
   Description: Secret Service API (local dev headers)
   Version: 0.21.7
   Requires: glib-2.0 >= 2.44, gio-2.0 >= 2.44, gio-unix-2.0 >= 2.44
   Libs: -L/usr/lib/x86_64-linux-gnu -l:libsecret-1.so.0
   Cflags: -I\${includedir}/libsecret-1
   PC
   ```
   要点：
   - 去掉上游 `.pc` 里的 `Requires.private: libgcrypt` —— 动态链接时不需要它，
     而 `libgcrypt` 的 dev 包通常也没装。
   - **用 `-l:libsecret-1.so.0` 直接链接系统 soname（不要 `-lsecret-1 -L<私有目录>`）**，
     否则链接器会把你的私有目录写进插件的 `RUNPATH`，产物绑死在个人路径上。

   构建时：
   ```bash
   export PKG_CONFIG_PATH="$HOME/.local/neilico-deps/pkgconfig:$PKG_CONFIG_PATH"
   flutter build linux --release
   ldd build/linux/x64/release/bundle/lib/libflutter_secure_storage_linux_plugin.so | grep secret
   # 期望：libsecret-1.so.0 => /usr/lib/x86_64-linux-gnu/libsecret-1.so.0
   ```

2. **clang ≥ 19 会因插件的旧 json.hpp 报错**：`flutter_secure_storage_linux` 内置了
   一份很旧的 nlohmann `json.hpp`，其中 `operator""_x` 带空格的写法在 clang ≥ 19 上触发
   `-Wdeprecated-literal-operator`；而 `linux/CMakeLists.txt` 的 `apply_standard_settings`
   给所有插件加了 `-Werror`，于是编译直接失败。
   本项目在**自己的** `linux/CMakeLists.txt` 里补了一条
   `-Wno-deprecated-literal-operator`（放在 `-Werror` 之后，必定生效），
   不去改第三方插件源码。升级 Flutter/插件后可复核该补丁是否还需要。

## 代码结构

```
lib/
├── main.dart                     # 入口：窗口初始化 + 恢复会话
├── app.dart                      # 主题/路由/顶层阶段切换（booting/loggedOut/ready）
├── theme/neilico_theme.dart      # NEILICO 设计令牌（颜色/间距/圆角，禁止硬编码颜色）
├── util/                         # json 容错取值、校验、时间格式化
├── models/                       # AuthSession / RemoteDesktopDevice / 授权策略 / 开关门禁
│   ├── device.dart
│   ├── device_policy.dart        # 每设备授权状态（含「需要的接口」约定）
│   └── device_switches.dart      # ★ 开关是否可用的**全部规则**（纯逻辑，可单测）
├── api/                          # 极简 REST 客户端 + 认证 / 远程桌面接口
├── services/
│   ├── app_controller.dart       # ChangeNotifier 状态容器（无第三方状态库）
│   ├── secure_token_store.dart   # 令牌进 OS 密钥库（libsecret/Keychain/DPAPI）
│   ├── session_store.dart        # 会话 + 地址的整体序列化
│   ├── connect_launcher.dart     # 调起 rustdesk://（内核未接入前的出口）
│   └── kernel/                   # 方案②进程边界的客户端侧骨架
└── screens/ widgets/             # 登录 / 设备网格 / 设备详情 + 复用组件
```

技术选型刻意保持轻量：**不引** Provider/Riverpod/Bloc、不引 GoRouter，
用 `ChangeNotifier` + `AnimatedBuilder` + `Navigator 1.0`。
唯一网络依赖是 `http`；`window_manager` 负责桌面窗口；`flutter_secure_storage`
负责令牌。

## 状态权威性与「置灰」约定（重要）

**开关的权威状态在后端**。客户端：

- 从 `GET /api/v1/remote-desktop/device-policies` 读取；接口未就绪（404/405/501）时
  把开关**全部置灰**并展示原因（`DevicePolicySnapshot.pending`），**绝不本地伪造状态**。
- 「可被远程」为 `false` 时，连接入口一并置灰（关闭时任何客户端都不得发起）。
- 其它置灰原因：设备离线/心跳过期、无虚拟 IP、无可用 Mesh 网络、服务器未就绪、
  设备未上报 RustDesk ID。

这些规则全部集中在 `lib/models/device_switches.dart` 的
`DeviceSwitchPanel.build()`，并被 `test/device_switches_test.dart` 逐条覆盖。

## 安全约定

- 令牌只经 `flutter_secure_storage` 落 OS 密钥库，**绝不明文落盘**；界面不显示、不回显。
- 只读公钥：连接参数只有 `public_key`，不出现私钥。
- 不实现自己的加密协议；画面链路交给内核（见 `../rust-core/README.md`）。

## 当前状态

- ✅ 登录（`/api/v1/auth/login`）、设备网格、服务器状态、设备详情与开关面板、连接入口
- ✅ 令牌安全存储 + 会话恢复 + 401 自动登出
- ⏳ 每设备的开关**读写**依赖控制面新接口，见 `docs/api-contract.md`（未就绪时已按约定置灰）
- ⏳ 内核进程：目前只做定位与状态展示，实际拉起等 `rust-core` 产物就绪
