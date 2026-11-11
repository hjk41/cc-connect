package core

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type configMenuState struct {
	mu                    sync.Mutex
	users, members, chats string
	allGroups             bool
}

func (s *configMenuState) AccessLists() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.users, s.members
}
func (s *configMenuState) SetAccessLists(users, members string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users, s.members = users, members
	return nil
}
func (s *configMenuState) ReplyAllChats() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chats, s.allGroups
}
func (s *configMenuState) SetReplyAllChats(chats string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chats = chats
	return nil
}

type configMenuCardPlatform struct {
	stubCardPlatform
	configMenuState
	navigation CardNavigationHandler
}

func (p *configMenuCardPlatform) SetCardNavigationHandler(h CardNavigationHandler) { p.navigation = h }

func (p *configMenuCardPlatform) ReplyCard(ctx context.Context, replyCtx any, card *Card) error {
	if err := p.stubCardPlatform.ReplyCard(ctx, replyCtx, card); err != nil {
		return err
	}
	return p.Reply(ctx, replyCtx, card.RenderText())
}

func (p *configMenuCardPlatform) SendCard(ctx context.Context, replyCtx any, card *Card) error {
	if err := p.stubCardPlatform.SendCard(ctx, replyCtx, card); err != nil {
		return err
	}
	return p.Send(ctx, replyCtx, card.RenderText())
}

type configMenuPlainPlatform struct {
	stubPlatformEngine
	configMenuState
}

func TestConfigHelpNavigationIncludesPlatformRuntimeSettings(t *testing.T) {
	p := &configMenuCardPlatform{
		stubCardPlatform: stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}},
		configMenuState:  configMenuState{users: "owner", members: "team", chats: "ocr"},
	}
	other := &stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "other"}}
	e := NewEngine("test", &stubAgent{}, []Platform{p, other}, "", LangChinese)
	e.SetConfigReloadFunc(func() (*ConfigReloadResult, error) { return &ConfigReloadResult{}, nil })
	card := e.handleCardNav("nav:/config", "test:chat:owner")
	if card == nil {
		t.Fatal("config navigation did not return a card")
	}
	text := card.RenderText()
	for _, want := range []string{"allow_from", "owner", "allow_all_members", "team", "reply_all_chats", "ocr", "+$this", "-$this", "管理员", "/config reload"} {
		if !strings.Contains(text, want) {
			t.Errorf("config card omitted %q: %s", want, text)
		}
	}
	otherText := e.handleCardNav("nav:/config", "other:chat:owner").RenderText()
	for _, unwanted := range []string{"allow_from", "allow_all_members", "reply_all_chats", "ocr"} {
		if strings.Contains(otherText, unwanted) {
			t.Errorf("capability/config from another platform leaked: %q", unwanted)
		}
	}
}

func TestConfigPlainTextListsPlatformSettingsAndLiveValues(t *testing.T) {
	p := &configMenuPlainPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}, configMenuState: configMenuState{users: "owner", chats: "first"}}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "test:chat:owner", ReplyCtx: "ctx"}
	e.cmdConfig(p, msg, nil)
	for _, want := range []string{"thinking_max_len", "allow_from", "allow_all_members", "reply_all_chats", "first", "none"} {
		if !strings.Contains(p.getSent()[0], want) {
			t.Errorf("plain config omitted %q", want)
		}
	}
	_ = p.SetReplyAllChats("second")
	e.cmdConfig(p, msg, nil)
	got := p.getSent()[1]
	if !strings.Contains(got, "second") || strings.Contains(got, "first") {
		t.Fatalf("menu contains stale runtime values: %s", got)
	}
}

func TestHelpMenusCoverEveryBuiltinCommand(t *testing.T) {
	listed := map[string]bool{"help": true}
	for _, group := range helpCardGroups() {
		for _, item := range group.items {
			listed[strings.TrimPrefix(item.command, "/")] = true
		}
	}
	for _, command := range builtinCommands {
		if !listed[command.id] {
			t.Errorf("help cards omit /%s", command.id)
		}
		for _, lang := range []Language{LangEnglish, LangChinese, LangTraditionalChinese, LangJapanese, LangSpanish} {
			if messages[MsgKey(command.id)][lang] == "" {
				t.Errorf("help description (%s) missing for /%s", lang, command.id)
			}
			if !strings.Contains("\n"+NewI18n(lang).T(MsgHelp), "\n/"+command.id+"\n") && !strings.Contains("\n"+NewI18n(lang).T(MsgHelp), "\n/"+command.id+" ") {
				t.Errorf("plain help (%s) omits /%s", lang, command.id)
			}
		}
	}
}

func TestConfigMenuExplainsEmptyListsAndGlobalReplies(t *testing.T) {
	p := &configMenuPlainPlatform{configMenuState: configMenuState{allGroups: true}}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	body, hint := e.formatConfigMenu(p)
	for _, want := range []string{"`allow_from` = `*`", "`allow_all_members` = `none`", "`reply_all_chats` = `none`", e.i18n.T(MsgReplyAllChatsGlobalEnabled)} {
		if !strings.Contains(body, want) {
			t.Errorf("menu omitted %q: %s", want, body)
		}
	}
	if strings.Contains(hint, "/config reload") {
		t.Error("reload advertised without a configured callback")
	}
}

type helpMenuAgent struct {
	stubAgent
	workDir string
}

func (a *helpMenuAgent) GetWorkDir() string { return a.workDir }

// Exercise every button as the platform would: nav/act callbacks update a card,
// while cmd callbacks enter through ReceiveMessage. External agents are stubbed;
// restart only queues a request, and diff operates on an empty temporary repo.
func TestHelpMenuButtonsOpenEveryDestination(t *testing.T) {
	previousVersion := CurrentVersion
	CurrentVersion = "dev" // The upgrade menu must not contact a release server.
	t.Cleanup(func() { CurrentVersion = previousVersion })
	for _, group := range helpCardGroups() {
		for _, item := range group.items {
			t.Run(group.key+item.command, func(t *testing.T) {
				p := &configMenuCardPlatform{stubCardPlatform: stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}}
				dir := t.TempDir()
				e := NewEngine("test", &helpMenuAgent{workDir: dir}, []Platform{p}, filepath.Join(dir, "sessions.json"), LangEnglish)
				e.SetAdminFrom("owner")
				e.onPlatformReady(p)
				t.Cleanup(func() {
					if err := e.Stop(); err != nil {
						t.Error(err)
					}
				})
				menu := p.navigation("nav:/help "+group.key, "test:oc_team:owner")
				button, ok := findCardAction(menu, item.action)
				if !ok {
					t.Fatal("help menu omitted its destination button")
				}
				kind, target, _ := strings.Cut(button.Value, ":")
				wantTarget := item.command
				if item.command == "/switch" {
					wantTarget = "/list" // Switch opens the session picker.
				}
				if target != wantTarget {
					t.Fatalf("button routes to %q, want %q", target, wantTarget)
				}
				switch kind {
				case "nav", "act":
					if item.command == "/doctor" {
						e.cancel() // Diagnose canceled external checks without network access.
					}
					card := p.navigation(button.Value, "test:oc_team:owner")
					if card == nil || card.Header == nil || card.Header.Title == "" {
						t.Fatal("help button returned no titled card")
					}
					if _, ok := findCardAction(card, "nav:/help"); !ok {
						t.Fatal("destination has no return-to-help button")
					}
					if err := p.ReplyCard(context.Background(), "ctx", card); err != nil {
						t.Fatal(err)
					}
				case "cmd":
					if item.command == "/diff" {
						if out, err := exec.Command("git", "init", "--quiet", dir).CombinedOutput(); err != nil {
							t.Fatalf("init diff workspace: %v: %s", err, out)
						}
					}
					e.ReceiveMessage(p, &Message{SessionKey: "test:oc_team:owner", Platform: "test", UserID: "owner", ChannelID: "oc_team", ChatType: "group", Content: target, ReplyCtx: "ctx"})
					if item.command == "/restart" {
						select {
						case request := <-RestartCh:
							if request.SessionKey != "test:oc_team:owner" {
								t.Fatalf("restart request targets another session: %+v", request)
							}
						default:
							t.Fatal("restart button did not queue a restart")
						}
					}
				default:
					t.Fatalf("unknown button action: %q", button.Value)
				}
				deadline := time.Now().Add(3 * time.Second)
				for len(p.getSent()) == 0 && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if sent := p.getSent(); len(sent) == 0 || strings.TrimSpace(sent[len(sent)-1]) == "" {
					t.Fatal("help button produced no visible response")
				}
			})
		}
	}
}
