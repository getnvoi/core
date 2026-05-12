package store

import (
	"context"
	"errors"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/getnvoi/core/pkg/agent"
)

func TestCreateSession(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "p")

	sess, err := st.CreateSession(ctx, proj.ID, "claude", "sonnet", "first chat")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.ID == "" {
		t.Fatal("ID not assigned")
	}
	if sess.Provider != "claude" {
		t.Fatalf("Provider: got %q want %q", sess.Provider, "claude")
	}
	if sess.LastAt.IsZero() || sess.CreatedAt.IsZero() {
		t.Fatal("timestamps zero")
	}
}

func TestGetSessionNotFound(t *testing.T) {
	st := freshStore(t)
	_, err := st.GetSession(context.Background(), "ghost")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("want ErrRecordNotFound, got %v", err)
	}
}

func TestListSessionsMostRecentFirst(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "p")

	s1, _ := st.CreateSession(ctx, proj.ID, "claude", "", "")
	s2, _ := st.CreateSession(ctx, proj.ID, "claude", "", "")
	s3, _ := st.CreateSession(ctx, proj.ID, "claude", "", "")
	// Touch s1 to make it most recent.
	if err := st.TouchSession(ctx, s1.ID); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	rows, err := st.ListSessions(ctx, proj.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len: got %d want 3", len(rows))
	}
	if rows[0].ID != s1.ID {
		t.Fatalf("most recent first: got %s want %s", rows[0].ID, s1.ID)
	}
	// Other two in reverse creation order (s3 before s2).
	if rows[1].ID != s3.ID || rows[2].ID != s2.ID {
		t.Fatalf("order: got %s,%s want %s,%s", rows[1].ID, rows[2].ID, s3.ID, s2.ID)
	}
}

func TestAppendMessageAssignsMonotonicSeq(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "p")
	sess, _ := st.CreateSession(ctx, proj.ID, "claude", "", "")

	msgs := []agent.Message{
		agent.Text("hi"),
		agent.Text("there"),
		agent.Text("friend"),
	}
	for _, m := range msgs {
		if _, err := st.AppendMessage(ctx, sess.ID, m); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	got, err := st.ListMessages(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len: got %d want 3", len(got))
	}
	for i, m := range got {
		if m.Content != msgs[i].Content {
			t.Fatalf("msg %d content: got %q want %q", i, m.Content, msgs[i].Content)
		}
	}
}

func TestAppendMessageInvalidKind(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "p")
	sess, _ := st.CreateSession(ctx, proj.ID, "claude", "", "")
	_, err := st.AppendMessage(ctx, sess.ID, agent.Message{Kind: "garbage"})
	if err == nil {
		t.Fatal("invalid kind: want error, got nil")
	}
}

func TestAppendMessagePersistsMetadata(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "p")
	sess, _ := st.CreateSession(ctx, proj.ID, "claude", "", "")
	m := agent.ToolResult("tool-123", "nvoi_plan", `{"status":"ok"}`, false)
	if _, err := st.AppendMessage(ctx, sess.ID, m); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := st.ListMessages(ctx, sess.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len: got %d want 1", len(got))
	}
	md := got[0].Metadata
	if md["tool_use_id"] != "tool-123" {
		t.Fatalf("tool_use_id: got %v want tool-123", md["tool_use_id"])
	}
	if md["name"] != "nvoi_plan" {
		t.Fatalf("name: got %v want nvoi_plan", md["name"])
	}
	if md["is_error"] != false {
		t.Fatalf("is_error: got %v want false", md["is_error"])
	}
}

func TestAppendMessageBumpsLastAt(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "p")
	sess, _ := st.CreateSession(ctx, proj.ID, "claude", "", "")
	before := sess.LastAt

	if _, err := st.AppendMessage(ctx, sess.ID, agent.Text("hello")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	after, err := st.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if !after.LastAt.After(before) {
		t.Fatalf("LastAt not bumped: before=%v after=%v", before, after.LastAt)
	}
}

func TestAppendMessageConcurrentSeqUnique(t *testing.T) {
	// SQLite serializes writers via the file write lock. Concurrent
	// AppendMessage from 5 goroutines × 4 messages = 20 messages with
	// unique seqs and no duplicates. Regression guard against any
	// future refactor that drops the COALESCE(MAX(seq))+1 atomicity.
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "p")
	sess, _ := st.CreateSession(ctx, proj.ID, "claude", "", "")

	const goroutines = 5
	const perGoroutine = 4
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*perGoroutine)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				if _, err := st.AppendMessage(ctx, sess.ID, agent.Text("m")); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Append: %v", err)
	}

	got, err := st.ListMessages(ctx, sess.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := goroutines * perGoroutine
	if len(got) != want {
		t.Fatalf("count: got %d want %d", len(got), want)
	}
	// All seqs must be 1..want with no duplicates.
	var rows []Message
	if err := st.DB().Where("session_id = ?", sess.ID).Order("seq ASC").Find(&rows).Error; err != nil {
		t.Fatalf("read raw seq: %v", err)
	}
	for i, r := range rows {
		if r.Seq != int64(i+1) {
			t.Fatalf("row %d seq: got %d want %d", i, r.Seq, i+1)
		}
	}
}

func TestNextSeq(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "p")
	sess, _ := st.CreateSession(ctx, proj.ID, "claude", "", "")

	// Empty session: next is 1.
	n, err := st.NextSeq(ctx, sess.ID)
	if err != nil {
		t.Fatalf("NextSeq empty: %v", err)
	}
	if n != 1 {
		t.Fatalf("empty session NextSeq: got %d want 1", n)
	}
	// After two appends: next is 3.
	st.AppendMessage(ctx, sess.ID, agent.Text("a"))
	st.AppendMessage(ctx, sess.ID, agent.Text("b"))
	n, _ = st.NextSeq(ctx, sess.ID)
	if n != 3 {
		t.Fatalf("after 2 appends NextSeq: got %d want 3", n)
	}
}
