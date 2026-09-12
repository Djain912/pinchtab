# extract testdata

Accessibility snapshots used by `TestResolve_FromSnapshotFixtures`. Each file is
the `nodes` array of a `pinchtab snap --json` capture, so the unit tests run
against the same node shape the e2e fixtures produce.

| testdata file          | fixture                                  |
| ---------------------- | ---------------------------------------- |
| `extract-product.json` | `tests/e2e/fixtures/extract-product.html` |
| `extract-article.json` | `tests/e2e/fixtures/extract-article.html` |

Authored to mirror the committed fixtures at PinchTab commit `c906674b`
(the HEAD when PIN-391 landed). Refresh from a Docker run when a snapshot
changes shape:

```
pinchtab open http://fixtures/extract-product.html
pinchtab snap --json > internal/extract/testdata/extract-product.json
```
