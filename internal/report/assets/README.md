# Vendored web assets

`mutation-test-elements.js` is the UMD bundle of [`mutation-testing-elements`](https://github.com/stryker-mutator/mutation-testing-elements), the web component used by `WriteHTML` to render the Stryker v2 report inside a self-contained HTML page.

Pinned version: **3.7.3**

To refresh:

```sh
curl -sL https://unpkg.com/mutation-testing-elements@<version>/dist/mutation-test-elements.js \
  -o internal/report/assets/mutation-test-elements.js
```

Then update the version above and run `go test ./internal/report/...` to confirm the embedded HTML still parses round-trip.

The bundle is embedded into the gomutants binary via `go:embed` (`internal/report/html.go`); it is not loaded over the network at report-render time.

## Known upstream quirk: the selected-mutant dot animation

3.7.3 writes the mutant id into the DOM raw:

```js
data-mutant-id="${e.id}"
```

but looks it up encoded:

```js
(e?.querySelector(`[${t}="${encodeURIComponent(n)}"] path animate`))?.beginElement()
```

Our ids (`internal/a/a.go:F:TYPE#1`) contain `/ : # ( ) ~ *`, which `encodeURIComponent` rewrites to `%2F %3A %23 …`, so that selector never matches and the `beginElement()` call no-ops. The effect is cosmetic — the pulse on the selected mutant's dot does not play. Both functional paths compare the raw string (`getAttribute` against `id.toString()`, and the lit repeat key), so selection, filtering and navigation are unaffected.

This is not caused by anything gomutants does at render time; it appeared when mutant ids stopped being bare integers, for which `encodeURIComponent` happened to be the identity. If a future bundle version fixes the mismatch, drop this note.
