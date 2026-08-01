(function (root, factory) {
  "use strict";
  const api = factory();
  if (typeof module === "object" && module.exports) module.exports = api;
  else root.OrgYAML = api;
})(typeof globalThis === "object" ? globalThis : this, function () {
  "use strict";

  const MAX_DEPTH = 64;
  const MAX_OUTPUT = 1 << 20;
  const MAX_INPUT = 1 << 20;
  const FALLBACK = "# YAML display unavailable\n";
  const YAML_PARSE_ERROR = "YAML parse error";
  const UNSAFE_YAML = "custom tags, anchors, aliases, and merge keys are disabled";
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

      const output = [];
      for (const name of Object.keys(value).sort()) {
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

  function canonicalValue(value, depth = 0, seen = new WeakSet()) {
    if (depth > MAX_DEPTH) throw new RangeError("maximum depth exceeded");
    if (value === null || typeof value === "string" || typeof value === "boolean") return value;
    if (typeof value === "number" && Number.isFinite(value)) return Object.is(value, -0) ? 0 : value;
    if (typeof value !== "object") throw new TypeError("payload is not JSON-compatible");
    if (seen.has(value)) throw new TypeError("payload contains a cycle");
    seen.add(value);
    try {
      if (Array.isArray(value)) return value.map(item => canonicalValue(item, depth + 1, seen));
      const prototype = Object.getPrototypeOf(value);
      if (prototype !== Object.prototype && prototype !== null) throw new TypeError("payload contains an unsupported object");
      const output = Object.create(null);
      for (const name of Object.keys(value).sort()) output[name] = canonicalValue(value[name], depth + 1, seen);
      return output;
    } finally {
      seen.delete(value);
    }
  }

  function canonicalJSON(value) {
    const text = JSON.stringify(canonicalValue(value));
    if (text.length > MAX_OUTPUT) throw new RangeError("maximum payload exceeded");
    return text;
  }

  function failure(format, path, line, message) {
    const where = line ? ` at ${path} (line ${line})` : ` at ${path}`;
    const label = format.toLowerCase() === "yaml" ? YAML_PARSE_ERROR : `${format.toUpperCase()} parse error`;
    return {ok: false, error: `${label}${where}: ${message}`};
  }

  function parseJSON(text) {
    if (text.length > MAX_INPUT) return failure("JSON", "$", 0, "payload exceeds 1 MiB");
    if (!text.trim()) return {ok: true, value: null};
    try {
      const value = JSON.parse(text);
      canonicalJSON(value);
      return {ok: true, value};
    } catch (error) {
      return failure("JSON", "$", 0, String(error?.message || "invalid JSON").slice(0, 240));
    }
  }

  function mappingColon(text) {
    let quoted = false;
    let escaped = false;
    for (let index = 0; index < text.length; index++) {
      const character = text[index];
      if (quoted) {
        if (escaped) escaped = false;
        else if (character === "\\") escaped = true;
        else if (character === '"') quoted = false;
      } else if (character === '"') quoted = true;
      else if (character === ":") return index;
    }
    return -1;
  }

  function parseKey(text, path, line) {
    const raw = text.trim();
    if (!raw) throw {path, line, message: "mapping key is empty"};
    if (raw === "<<") throw {path, line, message: UNSAFE_YAML};
    if (raw.startsWith('"')) {
      try {
        const value = JSON.parse(raw);
        if (typeof value !== "string") throw new Error();
        return value;
      } catch (_) {
        throw {path, line, message: "mapping key must be a JSON string"};
      }
    }
    if (/['{}\[\],]/.test(raw)) throw {path, line, message: "mapping key must be plain text or a double-quoted JSON string"};
    return raw;
  }

  function parseScalar(text, path, line) {
    const raw = text.trim();
    if (/^[!&*]/.test(raw) || raw === "|" || raw === ">") throw {path, line, message: UNSAFE_YAML};
    if (raw === "null" || raw === "~") return null;
    if (raw === "true") return true;
    if (raw === "false") return false;
    if (raw === "[]") return [];
    if (raw === "{}") return {};
    if (/^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$/.test(raw)) {
      const number = Number(raw);
      if (!Number.isFinite(number)) throw {path, line, message: "number must be finite"};
      return number;
    }
    if (raw.startsWith('"') || raw.startsWith("[") || raw.startsWith("{")) {
      try {
        const value = JSON.parse(raw);
        canonicalJSON(value);
        return value;
      } catch (_) {
        throw {path, line, message: "quoted or inline values must use valid JSON syntax"};
      }
    }
    return raw;
  }

  function parseYAML(text) {
    if (text.length > MAX_INPUT) return failure("YAML", "$", 1, "payload exceeds 1 MiB");
    if (!text.trim()) return {ok: true, value: null};
    const rows = text.replace(/\r\n?/g, "\n").split("\n");
    const tokens = [];
    for (let index = 0; index < rows.length; index++) {
      const row = rows[index];
      if (row.includes("\t")) return failure("YAML", "$", index + 1, "tabs are disabled; use spaces for indentation");
      const trimmed = row.trim();
      if (!trimmed || trimmed.startsWith("#")) continue;
      if (trimmed === "---" || trimmed === "..." || trimmed.startsWith("%")) return failure("YAML", "$", index + 1, "directives and document markers are disabled");
      if (/(^|[\s:])(?:[!&*][^\s]*)/.test(trimmed) || /:\s*[|>]\s*$/.test(trimmed)) return failure("YAML", "$", index + 1, UNSAFE_YAML);
      tokens.push({indent: row.length - row.trimStart().length, text: trimmed, line: index + 1});
    }
    if (!tokens.length) return {ok: true, value: null};
    if (tokens[0].indent !== 0) return failure("YAML", "$", tokens[0].line, "root value must not be indented");
    const rootText = tokens[0].text;
    const rootIsSequence = rootText === "-" || rootText.startsWith("- ");
    if (tokens.length === 1 && !rootIsSequence && (mappingColon(rootText) < 0 || /^[\[{\"]/.test(rootText))) {
      try {
        const value = canonicalValue(parseScalar(rootText, "$", tokens[0].line));
        canonicalJSON(value);
        return {ok: true, value};
      } catch (error) {
        return failure("YAML", error.path || "$", error.line || tokens[0].line, String(error.message || "invalid YAML").slice(0, 240));
      }
    }

    function block(position, indentation, path, depth) {
      if (depth > MAX_DEPTH) throw {path, line: tokens[position]?.line || 1, message: "maximum depth exceeded"};
      const sequence = tokens[position].text === "-" || tokens[position].text.startsWith("- ");
      const value = sequence ? [] : Object.create(null);
      let cursor = position;
      while (cursor < tokens.length && tokens[cursor].indent === indentation) {
        const token = tokens[cursor];
        if (sequence) {
          if (!(token.text === "-" || token.text.startsWith("- "))) throw {path, line: token.line, message: "cannot mix a sequence and mapping at one indentation"};
          const childPath = `${path}[${value.length}]`;
          const raw = token.text.slice(1).trim();
          if (raw) {
            const colon = mappingColon(raw);
            const mappingEntry = colon >= 0 && (colon === raw.length - 1 || /\s/.test(raw[colon + 1]));
            if (!mappingEntry) {
              value.push(parseScalar(raw, childPath, token.line));
              cursor++;
            } else {
              const item = Object.create(null);
              const name = parseKey(raw.slice(0, colon), childPath, token.line);
              const propertyPath = plainKey.test(name) ? `${childPath}.${name}` : `${childPath}[${JSON.stringify(name)}]`;
              const firstValue = raw.slice(colon + 1).trim();
              if (!firstValue) throw {path: propertyPath, line: token.line, message: "an inline sequence mapping value is required"};
              item[name] = parseScalar(firstValue, propertyPath, token.line);
              cursor++;
              if (cursor < tokens.length && tokens[cursor].indent > indentation) {
                const parsed = block(cursor, tokens[cursor].indent, childPath, depth + 1);
                if (Array.isArray(parsed.value) || parsed.value === null || typeof parsed.value !== "object") throw {path: childPath, line: tokens[cursor].line, message: "sequence mapping continuation must be a mapping"};
                for (const sibling of Object.keys(parsed.value)) {
                  if (Object.prototype.hasOwnProperty.call(item, sibling)) throw {path: childPath, line: tokens[cursor].line, message: "duplicate mapping key"};
                  item[sibling] = parsed.value[sibling];
                }
                cursor = parsed.next;
              }
              value.push(item);
            }
          } else {
            if (cursor + 1 >= tokens.length || tokens[cursor + 1].indent <= indentation) throw {path: childPath, line: token.line, message: "sequence item is empty"};
            const parsed = block(cursor + 1, tokens[cursor + 1].indent, childPath, depth + 1);
            value.push(parsed.value);
            cursor = parsed.next;
          }
          continue;
        }

        if (token.text === "-" || token.text.startsWith("- ")) throw {path, line: token.line, message: "cannot mix a mapping and sequence at one indentation"};
        const colon = mappingColon(token.text);
        if (colon < 0) throw {path, line: token.line, message: "mapping entry requires ':'"};
        const name = parseKey(token.text.slice(0, colon), path, token.line);
        const childPath = plainKey.test(name) ? `${path}.${name}` : `${path}[${JSON.stringify(name)}]`;
        if (Object.prototype.hasOwnProperty.call(value, name)) throw {path: childPath, line: token.line, message: "duplicate mapping key"};
        const raw = token.text.slice(colon + 1).trim();
        if (raw) {
          value[name] = parseScalar(raw, childPath, token.line);
          cursor++;
        } else if (cursor + 1 < tokens.length && tokens[cursor + 1].indent > indentation) {
          const parsed = block(cursor + 1, tokens[cursor + 1].indent, childPath, depth + 1);
          value[name] = parsed.value;
          cursor = parsed.next;
        } else {
          value[name] = null;
          cursor++;
        }
      }
      if (cursor < tokens.length && tokens[cursor].indent > indentation) throw {path, line: tokens[cursor].line, message: "unexpected indentation"};
      return {value, next: cursor};
    }

    try {
      const parsed = block(0, 0, "$", 0);
      if (parsed.next !== tokens.length) throw {path: "$", line: tokens[parsed.next].line, message: "inconsistent indentation"};
      const value = canonicalValue(parsed.value);
      canonicalJSON(value);
      return {ok: true, value};
    } catch (error) {
      return failure("YAML", error.path || "$", error.line || 1, String(error.message || "invalid YAML").slice(0, 240));
    }
  }

  function parse(format, text) {
    const source = String(text ?? "");
    if (String(format).toLowerCase() === "json") return parseJSON(source);
    if (String(format).toLowerCase() === "yaml") return parseYAML(source);
    return failure("payload", "$", 0, "format must be JSON or YAML");
  }

  return {render, parse, canonicalJSON, MAX_DEPTH, MAX_OUTPUT};
});
