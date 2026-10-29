# JSON and XML

These matchers read JSON and XML: a request body, a gRPC message as JSON, or any value a matcher gets that holds them, and the claims of a JWT.

| Go | Mock file | Matches |
|---|---|---|
| `JSONPath(path, m)`, `Field` | `jsonpath`, `field` | What a JSONPath selects, matched by `m`: `[jsonpath, $.id, [gt, 1]]`. |
| `EqualJSON(doc, opts...)`, `EQJ` | `equaljson`, `eqj` | JSON equal to `doc`. Options loosen it: see [JSON](#json). |
| `JSONSchema(schema, opts...)` | `jsonschema` | JSON that conforms to a JSON Schema. |
| `EqualXML(doc, opts...)`, `EQX` | `equalxml`, `eqx` | An equal XML document, by what it means: see [XML](#xml). |
| `XPath(expr, m, opts...)` | `xpath` | What an XPath 1.0 expression selects, matched by `m`. |
| `JWT(m)` | `jwt` | A JWT whose header and claims `m` matches, without checking its signature: see [JWTs](#jwts). |
| `SignedJWT(keys, m)` | `signedjwt` | The same, for a token signed by one of `keys` and within its lifetime. |

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

## JWTs

`[jwt, matcher]` decodes a JWT and matches `{"header": {...}, "claims": {...}}`, so `jsonpath` reads it. The value is the token or an `Authorization` header with it: `Bearer <token>`, or any other scheme, is read past.

```yaml
request:
  method: GET
  path: /orders
  header:
    Authorization: [jwt, [all, [
      [jsonpath, $.claims.scope, [contains, orders:read]],
      [jsonpath, $.header.alg, [equal, RS256]]
    ]]]
```

`jwt` doesn't check the signature: a token anyone signed matches. `[signedjwt, keys, matcher]` matches only a token signed by one of `keys`, a JWK Set or a JWK, as an object or JSON text, or PEM public keys or certificates, such as the key a GitHub App registers. It refuses `alg: none` and HMAC algorithms, a `kid` none of the keys has (keys without a `kid`, such as PEM ones, are tried in turn), a key whose JWK names another `alg`, and a token whose `exp` is past or `nbf` future. A private key in `keys` fails the mock as it loads.

```yaml
request:
  method: POST
  path: /app/installations/42/access_tokens
  header:
    Authorization: [signedjwt, "-----BEGIN PUBLIC KEY-----\n...\n-----END PUBLIC KEY-----\n", [jsonpath, $.claims.iss, [equal, "123456"]]]
```

A matcher can't read the server's own keys, so to check tokens the server's [`jwt` templates](/templating/#tokens) sign, load the key from a file with `template.keys` and give `signedjwt` its public half.

In Go: `JWT(m)` and `SignedJWT(keys, m)`, which panics when `keys` holds no public key.
