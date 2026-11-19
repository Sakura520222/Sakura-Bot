package media

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestImageContextConsumptionIsolationExpiryAndClear(t *testing.T) {
	s := NewStore(5 * time.Minute)
	now := time.Now()
	s.now = func() time.Time { return now }
	a, b := Scope{UserID: 1, ChatID: 10}, Scope{UserID: 2, ChatID: 10}
	for _, id := range []int{3, 1, 2} {
		if _, _, err := s.Add(a, Ref{FileID: "file", MessageID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if added, _, _ := s.Add(a, Ref{FileID: "file", MessageID: 2}); added {
		t.Fatal("duplicate accepted")
	}
	_, _, _ = s.Add(b, Ref{FileID: "other", MessageID: 1})
	lease, err := s.Begin(a, "session", "这是什么", false)
	if err != nil || len(lease.Refs) != 3 || lease.Refs[0].MessageID != 1 {
		t.Fatal("album order or pending association")
	}
	if _, err := s.Begin(a, "session", "repeat", false); err == nil {
		t.Fatal("duplicate lease")
	}
	s.Finish(lease, true)
	if l, _ := s.Begin(a, "session", "tell a joke", false); l != nil {
		t.Fatal("unrelated question reused images")
	}
	lease, _ = s.Begin(a, "session", "它是什么颜色", false)
	if lease == nil || len(lease.Refs) != 3 {
		t.Fatal("followup lost images")
	}
	s.Finish(lease, true)
	_, _, _ = s.Add(a, Ref{FileID: "new", MessageID: 4})
	lease, _ = s.Begin(a, "session", "new question", false)
	if len(lease.Refs) != 1 || lease.Refs[0].FileID != "new" {
		t.Fatal("new batch must replace active images")
	}
	s.Finish(lease, false)
	retry, _ := s.Begin(a, "session", "retry", false)
	if retry == nil {
		t.Fatal("failed request lost images")
	}
	s.ClearUser(1)
	s.Finish(retry, false)
	if l, _ := s.Begin(a, "session", "这是什么", true); l != nil {
		t.Fatal("clear resurrected images")
	}
	if l, _ := s.Begin(Scope{UserID: 2, ChatID: 11}, "s", "image", true); l != nil {
		t.Fatal("cross-chat image leak")
	}
	now = now.Add(301 * time.Second)
	s.Cleanup()
	if len(s.states) != 0 {
		t.Fatal("expired references remain")
	}
}

func TestImageContextConcurrentConsumptionAndLimits(t *testing.T) {
	s := NewStore(5 * time.Minute)
	scope := Scope{UserID: 1, ChatID: 1}
	for i := 0; i < 4; i++ {
		_, _, _ = s.Add(scope, Ref{FileID: "file", MessageID: i})
	}
	if _, _, err := s.Add(scope, Ref{FileID: "overflow", MessageID: 5}); err == nil {
		t.Fatal("count limit ignored")
	}
	var count atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := s.Begin(scope, "s", "question", false)
			if err == nil && l != nil {
				count.Add(1)
			}
		}()
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatal("images consumed concurrently")
	}
}

func TestImageDataValidation(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for _, format := range []string{"png", "jpeg"} {
		var data bytes.Buffer
		if format == "png" {
			_ = png.Encode(&data, img)
		} else {
			_ = jpeg.Encode(&data, img, nil)
		}
		url, err := ImageDataURL(data.Bytes())
		if err != nil || len(url) < 30 {
			t.Fatalf("valid %s rejected", format)
		}
		if _, err := ImageDataURL(data.Bytes()[:20]); err == nil {
			t.Fatal("corruption accepted")
		}
	}
	for _, data := range [][]byte{nil, []byte("GIF89a"), make([]byte, MaxImageBytes+1)} {
		if _, err := ImageDataURL(data); err == nil {
			t.Fatal("bad format/size accepted")
		}
	}
	var tooWide bytes.Buffer
	_ = png.Encode(&tooWide, image.NewGray(image.Rect(0, 0, 9000, 2)))
	if _, err := ImageDataURL(tooWide.Bytes()); err == nil {
		t.Fatal("pixel limit ignored")
	}
}
