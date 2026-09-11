package bridge

import (
	"context"
	"testing"
)

func scopedTabManager(ids ...string) *TabManager {
	tm := NewTabManager(context.Background(), nil, nil, nil, nil)
	for _, id := range ids {
		tm.tabs[id] = &TabEntry{Ctx: context.Background()}
	}
	return tm
}

func TestATabKeepsItsFirstCreatorAndTracksItsLastUser(t *testing.T) {
	tm := scopedTabManager("own", "shared", "adopted")
	tm.RecordTabScope("own", "session:s", true)
	tm.RecordTabScope("shared", "session:s", true)
	tm.RecordTabScope("shared", "session:t", false)
	tm.RecordTabScope("adopted", "session:s", true)
	tm.RecordTabScope("adopted", "session:t", true)
	tm.RecordTabScope("adopted", "session:s", false)
	tm.RecordTabScope("missing", "session:s", true)

	got := tm.TabsOnlyUsedByCreator("session:s")
	if len(got) != 2 || got[0] != "adopted" || got[1] != "own" {
		t.Fatalf("TabsOnlyUsedByCreator(session:s) = %v, want [adopted own]", got)
	}
	if got := tm.TabsOnlyUsedByCreator("session:t"); len(got) != 0 {
		t.Fatalf("TabsOnlyUsedByCreator(session:t) = %v, want none: t never created a tab", got)
	}
}
