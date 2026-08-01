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

const complexYAML = 'customer:\n  name: "Ada"\n  flags:\n    - true\n    - null\nitems:\n  -\n    qty: 2\n    sku: "A-1"\n  -\n    qty: 1\n    sku: "B-2"\n';
const parsedYAML = yaml.parse('yaml', complexYAML);
assert(parsedYAML.ok && parsedYAML.value.customer.name === 'Ada' && parsedYAML.value.items[1].sku === 'B-2', 'nested YAML parse failed');
const parsedJSON = yaml.parse('json', '{"items":[{"z":2,"a":1}],"enabled":true}');
assert(parsedJSON.ok && parsedJSON.value.items[0].a === 1, 'nested JSON parse failed');
assert(yaml.parse('yaml', '"hello"').value === 'hello', 'root YAML string failed');
assert(yaml.parse('yaml', '42').value === 42, 'root YAML number failed');
const rootArray = yaml.parse('yaml', '[1,{"x":true}]');
assert(rootArray.ok && rootArray.value[1].x === true, 'root YAML inline array failed');
const commonSequence = yaml.parse('yaml', 'items:\n  - sku: "A-1"\n    qty: 2\n  - sku: "B-2"\n    qty: 1\n');
assert(commonSequence.ok && commonSequence.value.items[1].qty === 1, 'common YAML mapping sequence failed');
const prototypeKey = yaml.parse('yaml', '"__proto__":\n  safe: true\n');
assert(prototypeKey.ok && Object.prototype.hasOwnProperty.call(prototypeKey.value, '__proto__') && prototypeKey.value.__proto__.safe === true, 'prototype-like key was lost or mutated the object prototype');
assert(yaml.parse('yaml', '   \n').ok && yaml.parse('yaml', '   \n').value === null, 'blank YAML must become null');
assert(yaml.parse('json', '').ok && yaml.parse('json', '').value === null, 'blank JSON must become null');
assert(yaml.canonicalJSON({z:2,a:{y:1,x:[3,2]}}) === '{"a":{"x":[3,2],"y":1},"z":2}', 'canonical JSON ordering mismatch');
for (const input of ['value: *secret', 'value: &anchor 1', 'value: !env HOME', '<<: {"a":1}', 'value: |\n  secret', 'a: 1\na: 2', 'a:\t1']) {
  const rejected = yaml.parse('yaml', input);
  assert(!rejected.ok && rejected.error.includes('$') && rejected.error.includes('line'), 'unsafe YAML was not rejected with path/line: ' + input);
}
const invalidJSON = yaml.parse('json', '{"a":]');
assert(!invalidJSON.ok && invalidJSON.error.includes('$') && invalidJSON.error.includes('JSON parse error'), 'JSON error lost root path');
assert(!yaml.parse('yaml', 'x'.repeat((1 << 20) + 1)).ok, 'YAML input size bound was not enforced');
`
	command := exec.Command(node, "-e", script, renderer)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("YAML renderer contract failed: %v\n%s", err, output)
	}
}
