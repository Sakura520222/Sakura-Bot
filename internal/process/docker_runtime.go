// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package process

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

const defaultRuntimeDir = "/opt/sakura"

// IsDocker 判断当前进程是否运行在 Docker 容器内。
func IsDocker() bool {
	if os.Getenv("SAKURA_DOCKER") == "1" {
		return true
	}
	fi, err := os.Stat("/.dockerenv")
	return err == nil && !fi.IsDir()
}

// FrontendFingerprint 计算 web/ 目录源码的确定性 SHA-256 指纹
// （排除 node_modules、dist、.vite、.git、符号链接和 *.tsbuildinfo，逐字节对齐 docker_runtime.py）。
func FrontendFingerprint(root string) (string, error) {
	webDir := filepath.Join(root, "web")
	h := sha256.New()
	if err := walkFrontendDir(webDir, webDir, h); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func walkFrontendDir(baseDir, curDir string, w io.Writer) error {
	entries, err := os.ReadDir(curDir)
	if err != nil {
		return err
	}
	var subDirs []string
	var files []string
	for _, e := range entries {
		name := e.Name()
		if e.Type()&fs.ModeSymlink != 0 {
			continue
		}
		fullPath := filepath.Join(curDir, name)
		info, lerr := os.Lstat(fullPath)
		if lerr != nil || info.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		if info.IsDir() {
			switch name {
			case "node_modules", "dist", ".vite", ".git":
				continue
			}
			subDirs = append(subDirs, name)
		} else {
			if strings.HasSuffix(name, ".tsbuildinfo") {
				continue
			}
			files = append(files, name)
		}
	}
	slices.Sort(subDirs)
	slices.Sort(files)

	for _, name := range files {
		fullPath := filepath.Join(curDir, name)
		rel, err := filepath.Rel(baseDir, fullPath)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return err
		}
		_, _ = w.Write([]byte(filepath.ToSlash(rel)))
		_, _ = w.Write([]byte{0})
		_, _ = w.Write(data)
		_, _ = w.Write([]byte{0})
	}
	for _, dir := range subDirs {
		if err := walkFrontendDir(baseDir, filepath.Join(curDir, dir), w); err != nil {
			return err
		}
	}
	return nil
}

// RecordImageBuild 在镜像构建阶段记录前端构建指纹（替代 docker_runtime.py record-image）。
func RecordImageBuild(root, runtimeDir string) error {
	if root == "" {
		root = "."
	}
	if runtimeDir == "" {
		runtimeDir = defaultRuntimeDir
	}
	pkgJSON := filepath.Join(root, "web", "package.json")
	if _, err := os.Stat(pkgJSON); err != nil {
		return nil
	}
	fp, err := FrontendFingerprint(root)
	if err != nil {
		return err
	}
	if err := writeDistMarker(root, fp); err != nil {
		return err
	}
	if os.Getenv("SAKURA_DOCKER") == "1" {
		if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(runtimeDir, "frontend.sha256"), []byte(fp), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(runtimeDir, "image-frontend.sha256"), []byte(fp), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// PrepareRuntime 在容器启动（挂载 /app 源码）时核对并恢复或重构建 WebUI 静态产物。
func PrepareRuntime(root, runtimeDir string) error {
	if root == "" {
		root = "."
	}
	if runtimeDir == "" {
		runtimeDir = defaultRuntimeDir
	}
	pkgJSON := filepath.Join(root, "web", "package.json")
	if fi, err := os.Stat(pkgJSON); err != nil || fi.IsDir() {
		return nil
	}
	fp, err := FrontendFingerprint(root)
	if err != nil {
		return err
	}
	distIndex := filepath.Join(root, "web", "dist", "index.html")
	if fi, err := os.Stat(distIndex); err == nil && !fi.IsDir() && readDistMarker(root) == fp {
		return nil
	}

	cacheDir := filepath.Join(runtimeDir, "web-dist")
	cacheIndex := filepath.Join(cacheDir, "index.html")
	if readRuntimeMarker(runtimeDir, "image-frontend") == fp {
		if fi, err := os.Stat(cacheIndex); err == nil && !fi.IsDir() {
			fmt.Println("恢复镜像中的 WebUI 构建产物...")
			if err := copyDirRecursive(cacheDir, filepath.Join(root, "web", "dist")); err != nil {
				return err
			}
			if err := writeDistMarker(root, fp); err != nil {
				return err
			}
			return recordRuntimeFrontend(runtimeDir, fp)
		}
	}

	fmt.Println("构建挂载源码的 WebUI...")
	npmBin, err := exec.LookPath("npm")
	if err != nil {
		npmBin = "npm"
	}
	webDir := filepath.Join(root, "web")
	installArgs := []string{"install", "--include=dev", "--package-lock=false"}
	if fi, err := os.Stat(filepath.Join(webDir, "package-lock.json")); err == nil && !fi.IsDir() {
		installArgs = []string{"ci", "--include=dev"}
	}
	cmdInstall := exec.Command(npmBin, installArgs...)
	cmdInstall.Dir = webDir
	cmdInstall.Stdout = os.Stdout
	cmdInstall.Stderr = os.Stderr
	if err := cmdInstall.Run(); err != nil {
		return fmt.Errorf("npm %s 失败: %w", installArgs[0], err)
	}

	cmdBuild := exec.Command(npmBin, "run", "build")
	cmdBuild.Dir = webDir
	cmdBuild.Stdout = os.Stdout
	cmdBuild.Stderr = os.Stderr
	if err := cmdBuild.Run(); err != nil {
		return fmt.Errorf("npm run build 失败: %w", err)
	}
	if err := writeDistMarker(root, fp); err != nil {
		return err
	}
	return recordRuntimeFrontend(runtimeDir, fp)
}

func distMarkerPath(root string) string {
	return filepath.Join(root, "web", "dist", ".build-fingerprint")
}

func readDistMarker(root string) string {
	b, err := os.ReadFile(distMarkerPath(root))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func writeDistMarker(root, fp string) error {
	p := distMarkerPath(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(fp), 0o644)
}

func readRuntimeMarker(runtimeDir, name string) string {
	b, err := os.ReadFile(filepath.Join(runtimeDir, name+".sha256"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func recordRuntimeFrontend(runtimeDir, fp string) error {
	if os.Getenv("SAKURA_DOCKER") != "1" {
		return nil
	}
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runtimeDir, "frontend.sha256"), []byte(fp), 0o644)
}

func copyDirRecursive(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
