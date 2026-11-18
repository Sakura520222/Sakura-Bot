// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package mtproto

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

var (
	// ErrAuthCancelled 表示用户在终端交互中输入 "cancel" 主动中止 UserBot 登录。
	ErrAuthCancelled = errors.New("userbot login cancelled by user")

	// ErrNonInteractiveTerminal 表示当前标准输入非交互式终端（如后台守护或管道关闭），拒绝无限阻塞等待验证码。
	ErrNonInteractiveTerminal = errors.New("non-interactive terminal: cannot read userbot login code from stdin")

	// ErrEmptyPhoneNumber 表示未配置 USERBOT_PHONE_NUMBER，无法发起首次短信/App 验证码登录。
	ErrEmptyPhoneNumber = errors.New("userbot phone number is empty")
)

// TerminalAuthenticator 实现基于终端标准输入输出的 UserBot 首次登录认证器：
//   - 支持输入验证码与可选两步验证（2FA SRP）密码；
//   - 输入 "cancel"（不区分大小写）立即中止；
//   - 空手机号或非 TTY 标准输入立即返回明确错误，绝不挂起进程。
type TerminalAuthenticator struct {
	PhoneNumber string
	Stdin       io.Reader
	Stdout      io.Writer
	AllowNonTTY bool

	mu     sync.Mutex
	reader *bufio.Reader
}

var _ auth.UserAuthenticator = (*TerminalAuthenticator)(nil)

// NewTerminalAuthenticator 创建终端交互认证器。
func NewTerminalAuthenticator(phone string, stdin io.Reader, stdout io.Writer) *TerminalAuthenticator {
	allowNonTTY := false
	if stdin == nil {
		stdin = os.Stdin
	} else if _, isOSFile := stdin.(*os.File); !isOSFile {
		// 测试注入的 strings.Reader / bytes.Buffer 允许直接按行读取，遇到 EOF 立即报错返回。
		allowNonTTY = true
	}
	if stdout == nil {
		stdout = os.Stdout
	}
	return &TerminalAuthenticator{
		PhoneNumber: strings.TrimSpace(phone),
		Stdin:       stdin,
		Stdout:      stdout,
		AllowNonTTY: allowNonTTY,
	}
}

// Phone 返回配置的国际格式手机号；为空时直接失败而不阻塞。
func (a *TerminalAuthenticator) Phone(_ context.Context) (string, error) {
	phone := strings.TrimSpace(a.PhoneNumber)
	if phone == "" {
		return "", ErrEmptyPhoneNumber
	}
	return phone, nil
}

// Code 在终端打印登录横幅并读取用户输入的验证码。
func (a *TerminalAuthenticator) Code(ctx context.Context, _ *tg.AuthSentCode) (string, error) {
	if err := a.ensureInteractive(); err != nil {
		return "", err
	}
	a.writePrompt(fmt.Sprintf(
		"\n%s\nUserBot 首次登录\n%s\n已向手机号 %s 发送验证码\n请在下方输入验证码（输入 'cancel' 取消登录）：\n%s\n验证码: ",
		strings.Repeat("=", 60),
		strings.Repeat("=", 60),
		a.PhoneNumber,
		strings.Repeat("-", 60),
	))

	line, err := a.readLine(ctx)
	if err != nil {
		return "", err
	}
	code := strings.TrimSpace(line)
	if strings.EqualFold(code, "cancel") {
		return "", ErrAuthCancelled
	}
	if code == "" {
		return "", errors.New("verification code is empty")
	}
	return code, nil
}

// Password 在检测到 2FA 时提示并读取两步验证密码。
func (a *TerminalAuthenticator) Password(ctx context.Context) (string, error) {
	if err := a.ensureInteractive(); err != nil {
		return "", err
	}
	a.writePrompt("检测到两步验证，请输入密码（输入 'cancel' 取消登录）：\n密码: ")

	line, err := a.readLine(ctx)
	if err != nil {
		return "", err
	}
	pwd := strings.TrimSpace(line)
	if strings.EqualFold(pwd, "cancel") {
		return "", ErrAuthCancelled
	}
	if pwd == "" {
		return "", errors.New("2FA password is empty")
	}
	return pwd, nil
}

// AcceptTermsOfService 默认接受服务条款。
func (a *TerminalAuthenticator) AcceptTermsOfService(_ context.Context, _ tg.HelpTermsOfService) error {
	return nil
}

// SignUp 拒绝未注册手机号自动建号（要求手机号已注册 Telegram）。
func (a *TerminalAuthenticator) SignUp(_ context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New("telegram phone number is not registered; sign up in official app first")
}

func (a *TerminalAuthenticator) ensureInteractive() error {
	if a.AllowNonTTY {
		return nil
	}
	r := a.Stdin
	if r == nil {
		r = os.Stdin
	}
	f, ok := r.(*os.File)
	if !ok {
		return nil
	}
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNonInteractiveTerminal, err)
	}
	if (fi.Mode() & os.ModeCharDevice) == 0 {
		return ErrNonInteractiveTerminal
	}
	return nil
}

func (a *TerminalAuthenticator) writePrompt(s string) {
	out := a.Stdout
	if out == nil {
		out = os.Stdout
	}
	_, _ = io.WriteString(out, s)
}

func (a *TerminalAuthenticator) readLine(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.reader == nil {
		in := a.Stdin
		if in == nil {
			in = os.Stdin
		}
		a.reader = bufio.NewReader(in)
	}
	r := a.reader
	a.mu.Unlock()

	type readResult struct {
		line string
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		s, err := r.ReadString('\n')
		if errors.Is(err, io.EOF) && len(s) > 0 {
			err = nil
		}
		ch <- readResult{line: s, err: err}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-ch:
		if res.err != nil {
			if errors.Is(res.err, io.EOF) {
				return "", fmt.Errorf("%w: unexpected EOF", ErrNonInteractiveTerminal)
			}
			return "", res.err
		}
		return strings.TrimRight(res.line, "\r\n"), nil
	}
}

// RunUserAuthFlow 执行 UserBot 验证码 + 可选 2FA SRP 密码（Auth().Password）登录流程。
func RunUserAuthFlow(ctx context.Context, authClient *auth.Client, authenticator auth.UserAuthenticator) error {
	if authClient == nil {
		return errors.New("nil auth client")
	}
	if authenticator == nil {
		return errors.New("nil user authenticator")
	}
	phone, err := authenticator.Phone(ctx)
	if err != nil {
		return err
	}
	sentClass, err := authClient.SendCode(ctx, phone, auth.SendCodeOptions{})
	if err != nil {
		return fmt.Errorf("send code: %w", err)
	}
	sent, ok := sentClass.(*tg.AuthSentCode)
	if !ok {
		return authClient.IfNecessary(ctx, auth.NewFlow(authenticator, auth.SendCodeOptions{}))
	}
	code, err := authenticator.Code(ctx, sent)
	if err != nil {
		return err
	}
	_, err = authClient.SignIn(ctx, phone, code, sent.PhoneCodeHash)
	if errors.Is(err, auth.ErrPasswordAuthNeeded) {
		pwd, pwdErr := authenticator.Password(ctx)
		if pwdErr != nil {
			return pwdErr
		}
		if _, pwdAuthErr := authClient.Password(ctx, pwd); pwdAuthErr != nil {
			return fmt.Errorf("2fa password sign in: %w", pwdAuthErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("sign in: %w", err)
	}
	return nil
}

func maskPhonePreview(phone string) string {
	p := strings.TrimSpace(phone)
	runes := []rune(p)
	if len(runes) <= 4 {
		return p
	}
	if len(runes) <= 7 {
		return string(runes[:2]) + "****" + string(runes[len(runes)-2:])
	}
	return string(runes[:3]) + "****" + string(runes[len(runes)-4:])
}
