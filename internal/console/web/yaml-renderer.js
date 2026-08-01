(function (root, factory) {
  "use strict";
  const api = factory();
  if (typeof module === "object" && module.exports) module.exports = api;
  else root.OrgYAML = api;
})(typeof globalThis === "object" ? globalThis : this, function () {
  "use strict";

  const MAX_DEPTH = 64;
  const MAX_OUTPUT = 1 << 20;
  const FALLBACK = "# YAML display unavailable\n";
  const plainKey = /^[A-Za-z_][A-Za-z0-9_-]*$/;
  const reservedKey = /^(?:null|true|false|yes|no|on|off)$/i;

  function scalar(value) {
    if (value === null) return {ok: true, text: "null"};
    if (typeof value === "string") return {ok: true, text: JSON.stringify(value)};
    if (typeof value === "boolean") return {ok: true, text: value ? "true" : "false"};
    if (typeof value === "number") {
      if (!Number.isFinite(value)) throw new TypeError("unsupported number");
      return {ok: true, text: Object.is(value, -0) ? "0" : String(value)};
    }
    if (Array.isArray(value) && value.length === 0) return {ok: true, text: "[]"};
    if (value && typeof value === "object") {
      const prototype = Object.getPrototypeOf(value);
      if ((prototype === Object.prototype || prototype === null) && Object.keys(value).length === 0) return {ok: true, text: "{}"};
    }
    return {ok: false, text: ""};
  }

  function key(value) {
    return plainKey.test(value) && !reservedKey.test(value) ? value : JSON.stringify(value);
  }

  function lines(value, depth, indentation, seen) {
    if (depth > MAX_DEPTH) throw new RangeError("maximum depth exceeded");
    const prefix = " ".repeat(indentation);
    const primitive = scalar(value);
    if (primitive.ok) return [prefix + primitive.text];
    if (typeof value !== "object" || value === null) throw new TypeError("unsupported value");

    const prototype = Object.getPrototypeOf(value);
    if (!Array.isArray(value) && prototype !== Object.prototype && prototype !== null) throw new TypeError("unsupported object");
    if (seen.has(value)) throw new TypeError("cyclic value");
    seen.add(value);
    try {
      if (Array.isArray(value)) {
        if (value.length === 0) return [prefix + "[]"];
        const output = [];
        for (const item of value) {
          const itemScalar = scalar(item);
          if (itemScalar.ok) output.push(prefix + "- " + itemScalar.text);
          else {
            output.push(prefix + "-");
            output.push(...lines(item, depth + 1, indentation + 2, seen));
          }
        }
        return output;
      }

      const keys = Object.keys(value).sort();
      if (keys.length === 0) return [prefix + "{}"];
      const output = [];
      for (const name of keys) {
        const child = value[name];
        const childScalar = scalar(child);
        if (childScalar.ok) output.push(prefix + key(name) + ": " + childScalar.text);
        else {
          output.push(prefix + key(name) + ":");
          output.push(...lines(child, depth + 1, indentation + 2, seen));
        }
      }
      return output;
    } finally {
      seen.delete(value);
    }
  }

  function render(value) {
    try {
      const text = lines(value, 0, 0, new WeakSet()).join("\n") + "\n";
      if (text.length > MAX_OUTPUT) throw new RangeError("maximum output exceeded");
      return {ok: true, text};
    } catch (_) {
      return {ok: false, text: FALLBACK};
    }
  }

  return {render, MAX_DEPTH, MAX_OUTPUT};
});
