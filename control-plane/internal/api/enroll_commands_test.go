package api

import (
	"strings"
	"testing"

	"neilico/control-plane/internal/config"
)

// 接入命令里的 Docker 镜像地址必须是「目标机真能拉到」的地址。
//
// 历史缺陷：命令写的是 `neilico-agent:local`。那只在本机构建过，镜像没进任何
// 仓库，于是别的机器执行同一条命令会得到
//
//	pull access denied for neilico-agent, repository does not exist
//
// （实测）。现在默认指向 ghcr.io，且可通过配置覆盖。
func TestEnrollCommandSetDockerUsesPullableImage(t *testing.T) {
	const base = "http://cp.example.com:13000"
	const token = "neilico-enroll.payload.sig"
	image := config.DefaultAgentImage

	cmds := enrollCommandSet(base+"/", token, image)

	// 逐段期望值：续行 = 空格 + 反斜杠 + 换行（cont 只定义一次）
	const cont = " \\\n"
	lines := []string{
		"docker run -d --name neilico-agent --restart unless-stopped",
		"  --network host --cap-add NET_ADMIN --device /dev/net/tun",
		"  -v neilico-agent-state:/var/lib/neilico-agent",
		"  -e NEILICO_TOKEN=" + token + " " + image,
	}
	// 命令开头必须先显式 `docker pull <镜像>`：`docker run` 是隐式拉取，
	// 用户看不出镜像来自哪里（实测反馈："还是没看出是往 github 拉取"）。
	want := "docker pull " + image + " &&" + cont + strings.Join(lines, cont)

	if cmds.Docker != want {
		t.Fatalf("Docker 命令不符\n--- 实际 ---\n%s\n--- 期望 ---\n%s", cmds.Docker, want)
	}
	t.Logf("渲染出的 Docker 接入命令:\n%s", cmds.Docker)

	// ── 回归护栏：绝不能出现只在本机存在的 tag ────────────────────────────
	if strings.Contains(cmds.Docker, "neilico-agent:local") {
		t.Fatal("命令又用回了本机 tag `neilico-agent:local`：别的机器必然 pull access denied")
	}
	if !strings.Contains(cmds.Docker, "ghcr.io/") {
		t.Fatal("默认镜像地址应当是可拉取的仓库地址（ghcr.io/...）")
	}
	if !strings.HasSuffix(cmds.Docker, image) {
		t.Fatalf("命令结尾应当是镜像地址 %q", image)
	}

	// ── 必须显式写出 pull，且 pull 在 run 之前（来源要一眼可见）──────────
	pullAt := strings.Index(cmds.Docker, "docker pull "+image)
	runAt := strings.Index(cmds.Docker, "docker run")
	if pullAt != 0 {
		t.Fatalf("命令应以 `docker pull %s` 开头，实际开头: %.40q", image, cmds.Docker)
	}
	if runAt < 0 || pullAt > runAt {
		t.Fatalf("docker pull 必须在 docker run 之前（pull=%d run=%d）", pullAt, runAt)
	}
	if !strings.Contains(cmds.Docker, "docker pull "+image+" &&") {
		t.Fatal("pull 与 run 之间应当用 `&&` 连接：拉取失败就不要启动容器")
	}

	// ── 独立于期望值的逐字节检查（转义写错会被这里抓到，非同义反复）──────
	if strings.Contains(cmds.Docker, `\\`) {
		t.Fatal("命令里出现了双反斜杠：Go 源码转义写错了")
	}
	if got := strings.Count(cmds.Docker, "\\\n"); got != len(lines) {
		t.Fatalf("续行（反斜杠+换行）应为 %d 处，实际 %d 处", len(lines), got)
	}
	if strings.Contains(cmds.Docker, "\t") || strings.Contains(cmds.Docker, "\r") {
		t.Fatal("命令里不应有制表符或回车（粘贴进 shell 会出问题）")
	}
	if !strings.Contains(cmds.Docker, "--device /dev/net/tun") ||
		!strings.Contains(cmds.Docker, "--cap-add NET_ADMIN") {
		t.Fatal("缺少建 WireGuard 接口必需的 NET_ADMIN / /dev/net/tun")
	}

	// ── 可配置：换 registry 时要真的换掉 ─────────────────────────────────
	custom := "registry.internal:5000/neilico/agent:1.2.3"
	if got := enrollCommandSet(base, token, custom).Docker; !strings.HasSuffix(got, custom) {
		t.Fatalf("自定义镜像地址未被采用: %s", got)
	}

	// ── 其余平台：Linux/macOS 共用 install.sh（控制面分发二进制，跨机本就可
	//    用），Windows 走 install.ps1 ────────────────────────────────────
	if cmds.Linux != cmds.MacOS {
		t.Fatal("Linux 与 macOS 应共用同一条 install.sh 命令")
	}
	if !strings.Contains(cmds.Linux, "/install.sh") || !strings.Contains(cmds.Linux, token) {
		t.Fatal("Linux/macOS 命令应引用 install.sh 且带上令牌")
	}
	if !strings.Contains(cmds.Windows, "install.ps1") || !strings.Contains(cmds.Windows, token) {
		t.Fatal("Windows 命令应引用 install.ps1 且带上令牌")
	}
	if strings.Contains(cmds.Docker, "//downloads") {
		t.Fatal("base 末尾斜杠未清理，出现了 //downloads")
	}
}

// 空镜像地址必须回落到默认值，否则会生成一条以空格结尾的废命令。
func TestAgentImageOrDefault(t *testing.T) {
	if got := agentImageOrDefault(""); got != config.DefaultAgentImage {
		t.Fatalf("空值应回落到 %q，实际 %q", config.DefaultAgentImage, got)
	}
	if got := agentImageOrDefault("  "); got != config.DefaultAgentImage {
		t.Fatalf("空白应回落到 %q，实际 %q", config.DefaultAgentImage, got)
	}
	if got := agentImageOrDefault(" example.com/x:1 "); got != "example.com/x:1" {
		t.Fatalf("应去掉首尾空白，实际 %q", got)
	}
}
