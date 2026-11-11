package handlers

import (
	"context"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/engine"
	"github.com/go-telegram/bot/models"
	"sync"
	"testing"
	"time"
)

func TestTurnAdmissionFinalizationAndLimits(t *testing.T) {
	p := NewAIPool(context.Background(), 2, 4, time.Minute)
	defer p.Close()
	p.MaxMessages = 2
	j, existing, e := p.AcquireScoped(1, "a")
	if e != nil || existing {
		t.Fatal(e)
	}
	defer j.Release()
	billed := 0
	reserve := func() contracts.QuotaCheckResult { billed++; return contracts.QuotaCheckResult{Allowed: true} }
	if _, ok := j.Admit(engine.TurnInput{Query: "first"}, "1", reserve); !ok {
		t.Fatal("initial rejected")
	}
	again, existing, _ := p.AcquireScoped(1, "a")
	if !existing || again != j {
		t.Fatal("split active turn")
	}
	j.Admit(engine.TurnInput{Query: "second"}, "2", reserve)
	if _, ok := j.Admit(engine.TurnInput{Query: "third"}, "3", reserve); ok || billed != 2 {
		t.Fatal("limit charged")
	}
	if in := j.Take(true); len(in) != 1 || in[0].Query != "second" {
		t.Fatal(in)
	}
	if len(j.Take(true)) != 0 || !j.Sealed() {
		t.Fatal("not atomically sealed")
	}
	next, existing, e := p.AcquireScoped(1, "a")
	if e != nil || existing || next == j {
		t.Fatal("new turn not admitted after closure")
	}
	next.Release()
}
func TestFinalizingRaceNoLostAcceptedInput(t *testing.T) {
	for n := 0; n < 100; n++ {
		p := NewAIPool(context.Background(), 1, 1, time.Second)
		j, _, _ := p.AcquireScoped(1, "a")
		j.Admit(engine.TurnInput{Query: "first"}, "1", func() contracts.QuotaCheckResult { return contracts.QuotaCheckResult{Allowed: true} })
		var wg sync.WaitGroup
		wg.Add(2)
		accepted := false
		var drained []engine.TurnInput
		go func() {
			defer wg.Done()
			_, accepted = j.Admit(engine.TurnInput{Query: "follow"}, "2", func() contracts.QuotaCheckResult { return contracts.QuotaCheckResult{Allowed: true} })
		}()
		go func() { defer wg.Done(); drained = j.Take(true) }()
		wg.Wait()
		if accepted && len(drained) != 1 {
			t.Fatal("accepted input lost at finalization")
		}
		j.Release()
		p.Close()
	}
}
func TestMembershipStatuses(t *testing.T) {
	for _, tc := range []struct {
		m    models.ChatMember
		want bool
	}{{models.ChatMember{Type: models.ChatMemberTypeMember}, true}, {models.ChatMember{Type: models.ChatMemberTypeAdministrator}, true}, {models.ChatMember{Type: models.ChatMemberTypeOwner}, true}, {models.ChatMember{Type: models.ChatMemberTypeLeft}, false}, {models.ChatMember{Type: models.ChatMemberTypeBanned}, false}, {models.ChatMember{Type: models.ChatMemberTypeRestricted, Restricted: &models.ChatMemberRestricted{IsMember: true}}, true}, {models.ChatMember{Type: models.ChatMemberTypeRestricted, Restricted: &models.ChatMemberRestricted{IsMember: false}}, false}} {
		if member(&tc.m) != tc.want {
			t.Fatal(tc)
		}
	}
}
