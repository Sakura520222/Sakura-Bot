// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

// composeconfig 在不启动容器的情况下验证 docker-compose.yml 解析后的端口绑定。
//
// 依赖宿主机 Docker Engine 与 Compose v2；不可用时整个包跳过。
package composeconfig

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// repoRoot 是仓库根目录，docker-compose.yml 从中复制到临时项目。
var repoRoot = filepath.Join("..", "..")

type composePort struct {
	HostIP    string `json:"host_ip"`
	Published string `json:"published"`
	Target    int    `json:"target"`
	Protocol  string `json:"protocol"`
}

type composeService struct {
	Image       string            `json:"image"`
	Ports       []composePort     `json:"ports"`
	Environment map[string]string `json:"environment"`
	Healthcheck struct {
		Test []string `json:"test"`
	} `json:"healthcheck"`
}

// renderCompose 在一次性目录中渲染 Compose 配置并返回目标服务。
func renderCompose(t *testing.T, values map[string]string, rootEnv, dataEnv string) composeService {
	t.Helper()
	project := t.TempDir()
	copyCompose(t, project)
	if err := os.MkdirAll(filepath.Join(project, "data"), 0o755); err != nil {
		t.Fatalf("创建 data 目录失败: %v", err)
	}
	writeFile(t, filepath.Join(project, "data", ".env"), dataEnv)
	writeFile(t, filepath.Join(project, ".env"), rootEnv)

	env := baseEnvironment()
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	cmd := exec.Command("docker", "compose", "--env-file", ".env", "config", "--format", "json")
	cmd.Dir = project
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("docker compose config 失败: %v\n%s", err, stderr.String())
	}
	var rendered struct {
		Services map[string]composeService `json:"services"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &rendered); err != nil {
		t.Fatalf("解析 docker compose config 输出失败: %v", err)
	}
	service, ok := rendered.Services["sakura-summary-bot"]
	if !ok {
		t.Fatalf("渲染结果缺少 sakura-summary-bot 服务")
	}
	return service
}

func copyCompose(t *testing.T, project string) {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(repoRoot, "docker-compose.yml"))
	if err != nil {
		t.Fatalf("读取 docker-compose.yml 失败: %v", err)
	}
	writeFile(t, filepath.Join(project, "docker-compose.yml"), string(source))
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatalf("写出 %s 失败: %v", name, err)
	}
}

// baseEnvironment 复制进程环境但剥离会影响渲染的 Compose 与 WebUI 变量，
// 使测试不受宿主机配置影响。
func baseEnvironment() []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "COMPOSE_") || strings.HasPrefix(key, "WEBUI_") ||
			key == "SAKURA_BOT_IMAGE" {
			continue
		}
		env = append(env, entry)
	}
	return env
}

// checkBinding 断言端口映射与应用环境符合部署约定。
func checkBinding(t *testing.T, service composeService, address, published string, target int) {
	t.Helper()
	if len(service.Ports) != 1 {
		t.Fatalf("期望恰好一个端口映射，实际 %d 个", len(service.Ports))
	}
	port := service.Ports[0]
	if port.HostIP != address {
		t.Errorf("host_ip = %q，期望 %q", port.HostIP, address)
	}
	if port.Published != published {
		t.Errorf("published = %q，期望 %q", port.Published, published)
	}
	if port.Target != target {
		t.Errorf("target = %d，期望 %d", port.Target, target)
	}
	if port.Protocol != "tcp" {
		t.Errorf("protocol = %q，期望 tcp", port.Protocol)
	}
	// 容器内监听所有接口，供 Docker 转发及同网络代理访问。
	if got := service.Environment["WEBUI_HOST"]; got != "0.0.0.0" {
		t.Errorf("WEBUI_HOST = %q，期望 0.0.0.0", got)
	}
	if got := service.Environment["WEBUI_PORT"]; got != strconv.Itoa(target) {
		t.Errorf("WEBUI_PORT = %q，期望 %q", got, strconv.Itoa(target))
	}
}

func TestUnsetAndEmptyValuesBindLoopback(t *testing.T) {
	requireDocker(t)
	cases := []map[string]string{
		{},
		{"WEBUI_BIND_ADDRESS": "", "WEBUI_PORT": "", "WEBUI_CONTAINER_PORT": ""},
	}
	for _, values := range cases {
		checkBinding(t, renderCompose(t, values, "", ""), "127.0.0.1", "8080", 8080)
	}
}

func TestExplicitAddressesAndCustomPorts(t *testing.T) {
	requireDocker(t)
	for _, address := range []string{"0.0.0.0", "192.0.2.10", "[::1]"} {
		service := renderCompose(t, map[string]string{
			"WEBUI_BIND_ADDRESS":   address,
			"WEBUI_PORT":           "18080",
			"WEBUI_CONTAINER_PORT": "9090",
		}, "", "")
		checkBinding(t, service, address, "18080", 9090)
	}
}

func TestHostAndContainerPortsAreIndependent(t *testing.T) {
	requireDocker(t)
	checkBinding(t, renderCompose(t, map[string]string{"WEBUI_PORT": "18080"}, "", ""),
		"127.0.0.1", "18080", 8080)
	checkBinding(t, renderCompose(t, map[string]string{"WEBUI_CONTAINER_PORT": "9090"}, "", ""),
		"127.0.0.1", "8080", 9090)
}

func TestRootEnvAndShellOverride(t *testing.T) {
	requireDocker(t)
	rootEnv := "WEBUI_BIND_ADDRESS=192.0.2.10\nWEBUI_PORT=18080\nWEBUI_CONTAINER_PORT=9090\n"
	checkBinding(t, renderCompose(t, nil, rootEnv, ""), "192.0.2.10", "18080", 9090)
	checkBinding(t, renderCompose(t, map[string]string{"WEBUI_BIND_ADDRESS": "127.0.0.1"}, rootEnv, ""),
		"127.0.0.1", "18080", 9090)
}

func TestExistingApplicationEnvAndPinnedImage(t *testing.T) {
	requireDocker(t)
	image := "ghcr.io/sakura520222/sakura-bot:sha-0631eccf167174e89151bc3360d95167f82dc317"
	service := renderCompose(t,
		map[string]string{"SAKURA_BOT_IMAGE": image},
		"",
		"WEBUI_BIND_ADDRESS=0.0.0.0\nWEBUI_HOST=127.0.0.1\nWEBUI_PORT=9090\nWEBUI_ENABLED=true\n")
	checkBinding(t, service, "127.0.0.1", "8080", 8080)
	if service.Image != image {
		t.Errorf("image = %q，期望钉住的 %q", service.Image, image)
	}
	if got := service.Environment["WEBUI_ENABLED"]; got != "true" {
		t.Errorf("WEBUI_ENABLED = %q，期望 true", got)
	}
	// 健康检查在容器内展开 WEBUI_PORT，使用容器端口。
	if len(service.Healthcheck.Test) < 2 || service.Healthcheck.Test[0] != "CMD-SHELL" {
		t.Fatalf("健康检查结构不符合预期: %v", service.Healthcheck.Test)
	}
	unescaped := strings.ReplaceAll(service.Healthcheck.Test[1], "$$", "$")
	if !strings.Contains(unescaped, "http://127.0.0.1:${WEBUI_PORT}/api/health") {
		t.Errorf("健康检查未使用容器端口: %s", service.Healthcheck.Test[1])
	}
}

// requireDocker 在没有 Docker Engine 或 Compose v2 的环境跳过本包。
func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("宿主机没有 docker 命令，跳过 Compose 配置校验")
	}
	cmd := exec.Command("docker", "compose", "version")
	if err := cmd.Run(); err != nil {
		t.Skip("Docker Compose v2 不可用，跳过 Compose 配置校验")
	}
}
