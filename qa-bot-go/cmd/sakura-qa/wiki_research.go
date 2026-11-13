package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/engine"
)

func withResearch(token, port string, stream func(context.Context, string, int64, string) <-chan engine.Chunk) func(context.Context, string, int64, string) <-chan engine.Chunk {
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return stream
	}
	secret := sha256.Sum256([]byte("sakura-wiki-research:v1:" + token))
	client := &http.Client{Timeout: 35 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context, text string, user int64, hint string) <-chan engine.Chunk {
		ctx = engine.WithWebResearch(ctx, func(ctx context.Context, tool, value string) string {
			const unavailable = `{"error":"联网检索暂不可用或已达到预算"}`
			if strings.TrimSpace(value) == "" || len(value) > 8192 {
				return unavailable
			}
			body, _ := json.Marshal(map[string]string{"value": value})
			request, err := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1:"+port+"/api/internal/wiki-research?operation="+strings.TrimPrefix(tool, "web_"), bytes.NewReader(body))
			if err != nil {
				return unavailable
			}
			request.Header.Set("Authorization", "Bearer "+hex.EncodeToString(secret[:]))
			request.Header.Set("Content-Type", "application/json")
			response, err := client.Do(request)
			if err != nil {
				return unavailable
			}
			defer response.Body.Close()
			data, err := io.ReadAll(io.LimitReader(response.Body, 200000))
			if err != nil || response.StatusCode != 200 || !json.Valid(data) {
				return unavailable
			}
			return string(data)
		})
		return stream(ctx, text, user, hint)
	}
}

type researchEngine struct {
	stream func(context.Context, string, int64, string) <-chan engine.Chunk
}

func (e researchEngine) ProcessQueryStream(ctx context.Context, text string, user int64, hint string) <-chan engine.Chunk {
	return e.stream(ctx, text, user, hint)
}
