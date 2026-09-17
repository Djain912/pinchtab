package bridge

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/pinchtab/pinchtab/internal/testbrowser"
)

// Labels that share a prefix, with the LONGER one first in document order —
// which is the arrangement that decides the bug, because a tie between equally
// specific matches falls to document order.
//
// "Delete"/"Delete all" is the pair worth naming: an agent told to delete one
// record and handed the control that deletes every record has not made a
// near-miss.
const textPrefixFixtureHTML = `<!doctype html>
<html><body>
<button id="saveexit">Save and exit</button>
<button id="save">Save</button>
<button id="deleteall">Delete all</button>
<button id="delete">Delete</button>
<button id="signin">Sign in with Google</button>
</body></html>`

func newTextPrefixFixture(t *testing.T) context.Context {
	t.Helper()
	chromePath := testbrowser.Path(t)

	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(), append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.UserDataDir(testbrowser.ProfileDir(t)),
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
	)...)
	ctx, cancelBrowser := chromedp.NewContext(alloc)
	ctx, cancelTimeout := context.WithTimeout(ctx, 30*time.Second)
	t.Cleanup(func() {
		cancelTimeout()
		cancelBrowser()
		cancelAlloc()
	})

	dataURL := "data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte(textPrefixFixtureHTML))
	if err := chromedp.Run(ctx,
		chromedp.Navigate(dataURL),
		chromedp.WaitVisible("#save", chromedp.ByID),
	); err != nil {
		t.Fatal(err)
	}
	return ctx
}

// A label that IS the query beats one that merely contains it.
//
// The candidate filter was one rung — named exact, testing includes — so "Save"
// and "Save and exit" were equally good answers to text:Save. Both are
// leaf-most, both weigh as controls, and the tie fell to document order, so a
// page listing the longer label first took the click. Measured through a real
// browser before this: text:Save clicked "Save and exit" and text:Delete
// clicked "Delete all", both reporting OK.
func TestTextSelectorPrefersTheLabelThatIsTheQuery(t *testing.T) {
	ctx := newTextPrefixFixture(t)

	for _, tc := range []struct {
		query string
		wantX string
	}{
		{query: "text:Save", wantX: "css:#save"},
		{query: "text:Delete", wantX: "css:#delete"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			want := resolve(t, ctx, tc.wantX)
			got := resolve(t, ctx, tc.query)
			if got != want {
				t.Errorf("%s resolved to %s, want %s — the control whose label is exactly the query",
					tc.query, describeNode(t, ctx, got), describeNode(t, ctx, want))
			}
		})
	}
}

// Containment still answers when nothing matches outright, so the shorthand that
// reaches a longer label by a fragment of it keeps working. Removing that would
// trade one silent wrong answer for a class of missing ones.
func TestTextSelectorStillReachesALongerLabelByAFragment(t *testing.T) {
	ctx := newTextPrefixFixture(t)

	want := resolve(t, ctx, "css:#signin")
	if got := resolve(t, ctx, "text:Sign"); got != want {
		t.Errorf("text:Sign resolved to %s, want button#signin (%s); no label is exactly \"Sign\", so containment is the only rung that can answer",
			describeNode(t, ctx, got), describeNode(t, ctx, want))
	}
}

// The exact rung reduces the match set like the size rule does; it must not
// change what a positional wrapper indexes into beyond that. first: over a query
// with an exact match still lands on that match rather than on an earlier
// element that merely contains it.
func TestExactPreferenceAppliesUnderPositionalWrappers(t *testing.T) {
	ctx := newTextPrefixFixture(t)

	want := resolve(t, ctx, "css:#delete")
	if got := resolve(t, ctx, "first:text:Delete"); got != want {
		t.Errorf("first:text:Delete resolved to %s, want button#delete (%s)",
			describeNode(t, ctx, got), describeNode(t, ctx, want))
	}
}
