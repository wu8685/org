package console

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestYAMLRendererIsStableBoundedAndSafeForJSONData(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable; browser renderer contract remains covered by UI asset tests")
	}
	renderer, err := filepath.Abs(filepath.Join("web", "yaml-renderer.js"))
	if err != nil {
		t.Fatal(err)
	}
	script := `
const yaml = require(process.argv[1]);
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const rendered = yaml.render({z:null,a:[3,{b:"<script>alert(1)</script>",a:true}],emptyObject:{},emptyArray:[],"on load":"line\nbreak"});
const expected = 'a:\n  - 3\n  -\n    a: true\n    b: "<script>alert(1)</script>"\nemptyArray: []\nemptyObject: {}\n"on load": "line\\nbreak"\nz: null\n';
assert(rendered.ok && rendered.text === expected, 'stable nested YAML mismatch: ' + rendered.text);
assert(yaml.render(null).text === 'null\n', 'null must remain visible');
assert(yaml.render([]).text === '[]\n' && yaml.render({}).text === '{}\n', 'empty collection mismatch');
for (const value of [Infinity, undefined, () => {}]) {
  const result = yaml.render(value);
  assert(!result.ok && result.text === '# YAML display unavailable\n', 'unsupported value did not use safe fallback');
}
const cyclic = {}; cyclic.self = cyclic;
assert(!yaml.render(cyclic).ok, 'cycle was accepted');
let deep = {}; let cursor = deep; for (let i = 0; i < 70; i++) cursor = cursor.next = {};
assert(!yaml.render(deep).ok, 'depth bound was not enforced');
assert(!yaml.render('x'.repeat((1 << 20) + 1)).ok, 'output bound was not enforced');
const hostile = {}; Object.defineProperty(hostile, 'value', {enumerable:true, get(){throw new Error('secret detail');}});
const fallback = yaml.render(hostile);
assert(!fallback.ok && !fallback.text.includes('secret detail'), 'fallback leaked internal error detail');
`
	command := exec.Command(node, "-e", script, renderer)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("YAML renderer contract failed: %v\n%s", err, output)
	}
}
