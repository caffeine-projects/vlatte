# JSON and XML

These matchers read JSON and XML: a request body, a gRPC message as JSON, or any value a matcher gets that holds them.

| Go | Mock file | Matches |
|---|---|---|
| `JSONPath(path, m)`, `Field` | `jsonpath`, `field` | What a JSONPath selects, matched by `m`: `[jsonpath, $.id, [gt, 1]]`. |
| `EqualJSON(doc, opts...)`, `EQJ` | `equaljson`, `eqj` | JSON equal to `doc`. Options loosen it: see [JSON](#json). |
| `JSONSchema(schema, opts...)` | `jsonschema` | JSON that conforms to a JSON Schema. |
| `EqualXML(doc, opts...)`, `EQX` | `equalxml`, `eqx` | An equal XML document, by what it means: see [XML](#xml). |
| `XPath(expr, m, opts...)` | `xpath` | What an XPath 1.0 expression selects, matched by `m`. |

## JSON

`[eqj, {...}]` matches JSON equal to the document given, in full. An object of options after it loosens that: `ignore_array_order: true` pairs list items up in any order, and `ignore_extra_elements: true` ignores members and list items the stub doesn't name.

`[jsonschema, schema]` matches JSON that conforms to a JSON Schema, an object or JSON text, of draft 2020-12 unless `$schema` or `{draft: 7}` says otherwise; it loads no `$ref` outside the schema.

In Go: `EqualJSON(doc, IgnoreArrayOrder(), IgnoreExtraElements())` and `JSONSchema(text, SchemaDraft(JSONSchemaDraft7))`.

```yaml
request:
  method: POST
  path: /pets
  body: [all, [
    [eqj, {name: Rex, tags: [a, b]}, {ignore_array_order: true, ignore_extra_elements: true}],
    [jsonpath, $.id, [regex, '^[0-9]+$']]
  ]]
```

## XML

`[eqx, '<order id="7"><item>1</item></order>']` matches an equal XML document, by what it means: attributes in any order, text trimmed, comments and indentation left out, names compared by namespace URI. Options: `ignore_child_order: true`, and `namespaces: strict` to compare prefixes too, or `none` to read no namespaces.

`[xpath, expression, matcher]` applies the matcher to what an XPath 1.0 expression selects: nothing is null, one result is itself, more are a list, so `[some, m]` matches any of them; an element is its XML. `{namespaces: {t: "urn:todo"}}` binds a prefix of the expression.

In Go: `EqualXML`, `IgnoreChildOrder`, `XMLNamespaces`, `XPath` and `XPathNamespace`.
