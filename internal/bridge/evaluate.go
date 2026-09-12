package bridge

import (
	"context"
	"encoding/json"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

func (b *Bridge) Evaluate(ctx context.Context, expression string, result any, opts EvalOpts) error {
	var chromedpOpts []chromedp.EvaluateOption
	if opts.AwaitPromise {
		chromedpOpts = append(chromedpOpts, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
			return p.WithAwaitPromise(true)
		})
	}
	return chromedp.Run(ctx, chromedp.Evaluate(expression, result, chromedpOpts...))
}

// EvaluateSubtype runs expression like Evaluate but also reports the returned
// RemoteObject's subtype (e.g. "promise"), which chromedp.Evaluate's by-value
// decode throws away. A Promise returned without AwaitPromise serialises to an
// empty object, so the subtype is the only thing that tells it apart from a
// genuine empty object — letting the caller hint --await-promise instead of
// silently handing back {}. The value decode and exception handling mirror
// chromedp.Evaluate, so behaviour is otherwise unchanged.
func (b *Bridge) EvaluateSubtype(ctx context.Context, expression string, result any, opts EvalOpts) (string, error) {
	var subtype string
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		p := runtime.Evaluate(expression).WithReturnByValue(true)
		if opts.AwaitPromise {
			p = p.WithAwaitPromise(true)
		}
		obj, exc, err := p.Do(ctx)
		if err != nil {
			return err
		}
		if exc != nil {
			return exc
		}
		subtype = string(obj.Subtype)
		if result == nil {
			return nil
		}
		value := []byte(obj.Value)
		if len(value) == 0 {
			value = []byte("null")
		}
		return json.Unmarshal(value, result)
	}))
	return subtype, err
}
