package main

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type fakeItem struct {
	id, owner string
	fetchErr  bool
}

func TestRunPendingAI(t *testing.T) {
	tests := []struct {
		name          string
		items         []fakeItem
		quota         map[string]int
		wantEvents    []string
		wantProcessed int
		wantErrors    int
	}{
		{
			name:          "processes within quota",
			items:         []fakeItem{{id: "a", owner: "alice"}, {id: "b", owner: "bob"}},
			quota:         map[string]int{"alice": 1, "bob": 1},
			wantEvents:    []string{"fetch a", "allow alice", "call a", "fetch b", "allow bob", "call b"},
			wantProcessed: 2,
		},
		{
			name:          "failed download does not consume quota",
			items:         []fakeItem{{id: "a", owner: "alice", fetchErr: true}, {id: "b", owner: "alice"}},
			quota:         map[string]int{"alice": 1},
			wantEvents:    []string{"fetch a", "fetch b", "allow alice", "call b"},
			wantProcessed: 1,
			wantErrors:    1,
		},
		{
			name:          "capped user is skipped for the rest of the run",
			items:         []fakeItem{{id: "a", owner: "alice"}, {id: "b", owner: "alice"}, {id: "c", owner: "alice"}, {id: "d", owner: "bob"}},
			quota:         map[string]int{"alice": 1, "bob": 1},
			wantEvents:    []string{"fetch a", "allow alice", "call a", "fetch b", "allow alice", "fetch d", "allow bob", "call d"},
			wantProcessed: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var events []string
			p, e := runPendingAI(context.Background(), tt.items, nil,
				func(it fakeItem) string { return it.owner },
				func(it fakeItem) ([]byte, error) {
					events = append(events, "fetch "+it.id)
					if it.fetchErr {
						return nil, errors.New("missing")
					}
					return []byte(it.id), nil
				},
				func(owner string) bool {
					events = append(events, "allow "+owner)
					if tt.quota[owner] <= 0 {
						return false
					}
					tt.quota[owner]--
					return true
				},
				func(it fakeItem, _ []byte) error {
					events = append(events, "call "+it.id)
					return nil
				},
			)
			if diff := cmp.Diff(tt.wantEvents, events); diff != "" {
				t.Errorf("events mismatch (-want +got):\n%s", diff)
			}
			if p != tt.wantProcessed || e != tt.wantErrors {
				t.Errorf("got processed=%d errors=%d, want %d %d", p, e, tt.wantProcessed, tt.wantErrors)
			}
		})
	}
}
