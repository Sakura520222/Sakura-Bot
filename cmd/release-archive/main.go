// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

// release-archive 构建并校验发布提交的可安装源码附件（ZIP 与 tar.gz）。
//
// 用法：
//
//	go run ./cmd/release-archive build  --directory <dir> [--revision HEAD] [--extract-to <dir>]
//	go run ./cmd/release-archive verify --directory <dir> [--extract-to <dir>]
//
// build 结束前同样执行完整校验，保证产物可安装。附件只包含受版本控制的
// Go 运行时、WebUI 源码与部署资源；data/ 下仅发布公开模板。
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// publicData 是 data/ 下随附件发布的模板文件。
var publicData = map[string]bool{
	"data/.env.example":        true,
	"data/config.example.json": true,
	"data/poll_prompt.txt":     true,
	"data/prompt.txt":          true,
	"data/qa_persona.txt":      true,
}

// required 是附件必须包含的最小文件集，缺失即视为打包配置回归。
var required = func() map[string]bool {
	m := make(map[string]bool, len(publicData)+24)
	for name := range publicData {
		m[name] = true
	}
	for _, name := range []string{
		".dockerignore",
		"Dockerfile",
		"docker-compose.yml",
		"docker-entrypoint.sh",
		"VERSION",
		"go.mod",
		"go.sum",
		"cmd/sakura-main/main.go",
		"cmd/sakura-qa/main.go",
		"cmd/release-archive/main.go",
		"internal/db/guard/schema.sql",
		"internal/guard/opencc/LICENSE",
		"internal/guard/opencc/NOTICE",
		"internal/tools/tool_schemas.json",
		"web/package.json",
		"web/package-lock.json",
		"web/index.html",
		"web/src/main.ts",
		"scripts/deploy.sh",
		"scripts/smoke_image.sh",
		"deploy/install-deployer.sh",
		"docs/release-archives.md",
	} {
		m[name] = true
	}
	return m
}()

const revisionFile = "RELEASE_REVISION"

// excludedParts 是任何路径段都不允许出现的目录名。
var excludedParts = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "bin": true,
	"sessions": true, "logs": true, "__pycache__": true,
}

// blockedSuffix 是除公开模板外禁止随附件发布的扩展名。
var blockedSuffix = map[string]bool{
	".db": true, ".sqlite": true, ".sqlite3": true,
	".session": true, ".pem": true, ".key": true, ".exe": true,
}

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.+-]+)?$`)

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "release-archive: "+format+"\n", args...)
	os.Exit(1)
}

// git 执行一条 git 命令并返回去除了首尾空白的输出。
func git(args ...string) string {
	cmd := exec.Command("git", args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fail("git %s 执行失败: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(stdout.String())
}

// sourcePaths 返回参与打包的顶层路径集合与完整文件清单，并完成安全审计。
func sourcePaths(revision string) ([]string, map[string]bool) {
	roots := map[string]bool{}
	for _, name := range strings.Split(git("ls-tree", "--name-only", revision), "\n") {
		if name != "" {
			roots[name] = true
		}
	}
	delete(roots, ".github")
	delete(roots, ".sakura")
	delete(roots, "data")

	paths := make([]string, 0, len(roots)+len(publicData))
	for name := range roots {
		paths = append(paths, name)
	}
	for name := range publicData {
		if !roots[name] {
			paths = append(paths, name)
		}
	}
	sort.Strings(paths)

	entries := git(append([]string{"ls-tree", "-r", "-z", revision, "--"}, paths...)...)
	files := map[string]bool{}
	for _, entry := range strings.Split(entries, "\x00") {
		if entry == "" {
			continue
		}
		header, name, ok := strings.Cut(entry, "\t")
		if !ok {
			fail("无法解析 git ls-tree 条目: %q", entry)
		}
		blocked := false
		for _, part := range strings.Split(name, "/") {
			if excludedParts[part] {
				blocked = true
			}
		}
		if !publicData[name] {
			base := path.Base(name)
			if strings.HasPrefix(base, ".env") ||
				strings.HasPrefix(base, "bot_session") ||
				strings.HasPrefix(base, "user_session") ||
				blockedSuffix[path.Ext(name)] {
				blocked = true
			}
		}
		modeType := strings.Fields(header)[0]
		if (modeType != "100644" && modeType != "100755") ||
			slicesContains(strings.Split(name, "/"), revisionFile) || blocked {
			fail("发布源码中出现意外路径: %s", name)
		}
		files[name] = true
	}
	var missing []string
	for name := range required {
		if !files[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		fail("缺少发布必需文件: %v", missing)
	}
	return paths, files
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sumHex(sum [sha256.Size]byte) string {
	return hex.EncodeToString(sum[:])
}

func fileDigest(name string) string {
	data, err := os.ReadFile(name)
	if err != nil {
		fail("读取 %s 失败: %v", name, err)
	}
	return sumHex(sha256.Sum256(data))
}

func checksums(archives []string) string {
	var builder strings.Builder
	for _, archive := range archives {
		fmt.Fprintf(&builder, "%s  %s\n", fileDigest(archive), filepath.Base(archive))
	}
	return builder.String()
}

// build 用 git archive 生成两种格式的附件并写出 SHA256SUMS。
func build(revision string, paths []string, archives []string, checksumFile, prefix string) {
	if err := os.MkdirAll(filepath.Dir(checksumFile), 0o755); err != nil {
		fail("创建输出目录失败: %v", err)
	}
	for _, archive := range archives {
		kind := "tar.gz"
		if strings.HasSuffix(archive, ".zip") {
			kind = "zip"
		}
		virtual := fmt.Sprintf("%s/%s:%s\n", prefix, revisionFile, revision)
		args := []string{
			"-c", "tar.umask=0022", "archive",
			"--format=" + kind,
			"--prefix=" + prefix + "/",
			"--add-virtual-file=" + virtual,
			"--output=" + archive,
			revision,
			"--",
		}
		git(append(args, paths...)...)
	}
	if err := os.WriteFile(checksumFile, []byte(checksums(archives)), 0o644); err != nil {
		fail("写出 SHA256SUMS 失败: %v", err)
	}
}

// entry 是附件成员的统一表示，内容已全部载入内存。
type entry struct {
	name   string
	isDir  bool
	isFile bool
	mode   uint32
	data   []byte
}

// readEntries 解析附件，返回按名称原序排列的全部成员。
func readEntries(archive string) []entry {
	raw, err := os.ReadFile(archive)
	if err != nil {
		fail("读取附件 %s 失败: %v", archive, err)
	}
	var entries []entry
	if strings.HasSuffix(archive, ".zip") {
		reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			fail("打开 ZIP %s 失败: %v", archive, err)
		}
		for _, file := range reader.File {
			// Windows 版 git 的 ZIP 条目可能缺少 POSIX 类型位，视为普通文件。
			isFile := file.Mode()&fs.ModeType == 0
			mode := uint32(0)
			if isFile {
				mode = uint32(file.Mode().Perm())
			}
			entries = append(entries, entry{
				name:   file.Name,
				isDir:  strings.HasSuffix(file.Name, "/"),
				isFile: isFile,
				mode:   mode,
				data:   readZipMember(file, archive),
			})
		}
		return entries
	}
	gzipReader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		fail("打开 tar.gz %s 失败: %v", archive, err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			fail("读取 tar 成员失败: %v", err)
		}
		if header.Typeflag == tar.TypeDir {
			entries = append(entries, entry{name: header.Name, isDir: true})
			continue
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			// git archive 写入的 pax 全局头不是归档内容，与 Python tarfile 一致跳过。
			continue
		}
		if header.Typeflag != tar.TypeReg {
			fail("附件 %s 中出现非普通文件成员: %s", path.Base(archive), header.Name)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			fail("读取 tar 成员 %s 失败: %v", header.Name, err)
		}
		entries = append(entries, entry{
			name:   header.Name,
			isFile: true,
			mode:   uint32(header.Mode & 0o777),
			data:   data,
		})
	}
	return entries
}

func readZipMember(file *zip.File, archive string) []byte {
	if strings.HasSuffix(file.Name, "/") {
		return nil
	}
	handle, err := file.Open()
	if err != nil {
		fail("读取 ZIP 成员 %s 失败: %v", file.Name, err)
	}
	defer handle.Close()
	data, err := io.ReadAll(handle)
	if err != nil {
		fail("读取 ZIP 成员 %s 失败: %v", file.Name, err)
	}
	_ = archive
	return data
}

// verify 校验附件清单、摘要与可安装性，可选解包到指定目录。
func verify(archives []string, checksumFile, prefix string, expected map[string]bool, revision string, extractTo string) {
	stored, err := os.ReadFile(checksumFile)
	if err != nil {
		fail("读取 SHA256SUMS 失败: %v", err)
	}
	if string(stored) != checksums(archives) {
		fail("附件 SHA-256 摘要不匹配")
	}

	expectedFiles := map[string]bool{prefix + "/" + revisionFile: true}
	for name := range expected {
		expectedFiles[prefix+"/"+name] = true
	}
	expectedDirs := map[string]bool{}
	for name := range expectedFiles {
		for dir := path.Dir(name); dir != "." && dir != "/"; dir = path.Dir(dir) {
			expectedDirs[dir] = true
		}
	}
	revisionDigest := sumHex(sha256.Sum256([]byte(revision + "\n")))

	manifests := make([]map[string]string, 0, len(archives))
	for index, archive := range archives {
		files := map[string]string{}
		var extracted []entry
		for _, member := range readEntries(archive) {
			if member.isDir {
				if !expectedDirs[strings.TrimSuffix(member.name, "/")] {
					fail("附件 %s 中出现意外目录: %s", path.Base(archive), member.name)
				}
				continue
			}
			if _, duplicate := files[member.name]; duplicate || !expectedFiles[member.name] || !member.isFile {
				fail("附件 %s 中出现意外文件: %s", path.Base(archive), member.name)
			}
			files[member.name] = sumHex(sha256.Sum256(member.data))
			extracted = append(extracted, member)
		}
		for name := range expectedFiles {
			if _, ok := files[name]; !ok {
				fail("附件 %s 缺少文件: %s", path.Base(archive), name)
			}
		}
		if files[prefix+"/"+revisionFile] != revisionDigest {
			fail("附件 %s 中的发布修订不正确", path.Base(archive))
		}
		if extractTo != "" {
			target := filepath.Join(extractTo, map[bool]string{true: "zip", false: "tar"}[index == 0])
			extractMembers(target, extracted)
		}
		manifests = append(manifests, files)
	}
	if len(manifests) == 2 && !manifestEqual(manifests[0], manifests[1]) {
		fail("ZIP 与 tar.gz 的文件内容不一致")
	}
	fmt.Printf("已校验 SHA-256 与每个附件的 %d 个文件（%s）\n", len(expectedFiles), revision)
}

func manifestEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for name, digest := range left {
		if right[name] != digest {
			return false
		}
	}
	return true
}

// extractMembers 将已通过清单校验的成员写出为普通文件。成员名称均已通过
// 白名单校验，不存在路径穿越风险。
func extractMembers(target string, entries []entry) {
	if err := os.MkdirAll(target, 0o755); err != nil {
		fail("创建解包目录失败: %v", err)
	}
	for _, member := range entries {
		full := filepath.Join(target, filepath.FromSlash(member.name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			fail("创建子目录失败: %v", err)
		}
		mode := member.mode
		if mode == 0 {
			mode = 0o644
		}
		if err := os.WriteFile(full, member.data, os.FileMode(mode)); err != nil {
			fail("写出附件成员 %s 失败: %v", full, err)
		}
	}
}

func main() {
	if len(os.Args) < 2 {
		fail("用法: release-archive <build|verify> --directory <dir> [--revision HEAD] [--extract-to <dir>]")
	}
	command := os.Args[1]
	if command != "build" && command != "verify" {
		fail("未知命令: %s（可用: build, verify）", command)
	}
	flags := flag.NewFlagSet(command, flag.ExitOnError)
	revision := flags.String("revision", "HEAD", "发布修订")
	directory := flags.String("directory", "", "附件输出或读取目录")
	extractTo := flags.String("extract-to", "", "校验后解包目录")
	if err := flags.Parse(os.Args[2:]); err != nil {
		fail("解析参数失败: %v", err)
	}
	if *directory == "" {
		fail("必须提供 --directory")
	}

	resolved := git("rev-parse", "--verify", *revision+"^{commit}")
	version := git("show", resolved+":VERSION")
	if !versionPattern.MatchString(version) {
		fail("无法从 VERSION 确定发布版本: %q", version)
	}
	prefix := "Sakura-Bot-v" + version
	archives := []string{
		filepath.Join(*directory, prefix+".zip"),
		filepath.Join(*directory, prefix+".tar.gz"),
	}
	checksumFile := filepath.Join(*directory, "SHA256SUMS")

	paths, expected := sourcePaths(resolved)
	if command == "build" {
		build(resolved, paths, archives, checksumFile, prefix)
	}
	verify(archives, checksumFile, prefix, expected, resolved, *extractTo)
}
