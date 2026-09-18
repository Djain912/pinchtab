package bridge

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	bridgecdpops "github.com/pinchtab/pinchtab/internal/bridge/cdpops"
	"github.com/pinchtab/pinchtab/internal/testbrowser"
)

// The three ways CSS hides an element reach a descendant differently, which is
// why one of them was slipping through.
//
// display:none leaves the child no box, so the width/height check catches it.
// visibility:hidden inherits, so the child's own computed style reports it.
// opacity does NEITHER — the child keeps a real box and a computed opacity of
// 1 — so a button inside an opacity:0 panel read as visible and was clicked.
// Measured before the fix: `click #ancestoropacity` returned OK.
//
// nested is the case that requires walking past the first ancestor: opacity
// multiplies down the tree, so an inner opacity:1 cannot undo an outer 0.
const opacityFixtureHTML = `<!doctype html><html><body>
<button id="normal">Normal</button>
<div style="opacity:0.5"><button id="fading">MidFade</button></div>
<div style="opacity:0.01"><button id="barely">Barely</button></div>
<div style="opacity:0"><button id="ancestorzero">AncestorZero</button></div>
<div style="opacity:0"><div style="opacity:1"><button id="nested">NestedReset</button></div></div>
<div style="visibility:hidden"><button id="ancestorhidden">AncestorHidden</button></div>
</body></html>`

func TestPointerRefusesAnElementHiddenByAnAncestorsOpacity(t *testing.T) {
	chromePath := testbrowser.Path(t)
	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(), append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
	)...)
	defer cancelAlloc()
	ctx, cancel := chromedp.NewContext(alloc)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, 30*time.Second)
	defer cancelTimeout()

	dataURL := "data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte(opacityFixtureHTML))
	if err := chromedp.Run(ctx, chromedp.Navigate(dataURL)); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		selector   string
		wantHidden bool
		why        string
	}{
		{selector: "#normal", wantHidden: false, why: "nothing hides it"},
		{selector: "#fading", wantHidden: false, why: "a half-faded panel is still on screen; only a fully transparent one is not"},
		{selector: "#barely", wantHidden: false, why: "faint is not absent — the threshold is zero, not a judgement about legibility"},
		{selector: "#ancestorzero", wantHidden: true, why: "an opacity:0 panel hides what is inside it"},
		{selector: "#nested", wantHidden: true, why: "opacity multiplies down the tree, so an inner opacity:1 cannot undo an outer 0"},
		{selector: "#ancestorhidden", wantHidden: true, why: "visibility inherits, and was already refused"},
	} {
		t.Run(tc.selector, func(t *testing.T) {
			node, err := firstNodeBySelector(ctx, tc.selector)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = bridgecdpops.PointerPointForNode(ctx, int64(node.BackendNodeID), false)
			gotHidden := errors.Is(err, bridgecdpops.ErrElementHidden)

			switch {
			case tc.wantHidden && !gotHidden:
				t.Errorf("%s was accepted as clickable (err=%v); %s", tc.selector, err, tc.why)
			case !tc.wantHidden && gotHidden:
				t.Errorf("%s was refused as hidden; %s", tc.selector, tc.why)
			case !tc.wantHidden && err != nil:
				t.Errorf("%s failed for an unrelated reason: %v", tc.selector, err)
			}
		})
	}
}
