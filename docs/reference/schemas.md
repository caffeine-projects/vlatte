# Mock file schemas

Mock files have JSON Schemas, served at stable addresses, so editors complete and check them as you write:

| Mock files | Schema |
|---|---|
| HTTP | [`https://caffeine-projects.github.io/vlatte/schemas/http.schema.json`](/schemas/http.schema.json){target="_self"} |
| gRPC | [`https://caffeine-projects.github.io/vlatte/schemas/grpc.schema.json`](/schemas/grpc.schema.json){target="_self"} |

Each schema's `$id` is its address. They describe the format the decoder reads: tests keep the schemas in step with it. The server never reads them; a file a schema accepts can still fail to load, such as one that names a body file that isn't there, and the error says why.

The admin API serves the HTTP schema too, at `GET /schema`.

## YAML

Start a YAML mock file with a comment naming its schema:

```yaml
# yaml-language-server: $schema=https://caffeine-projects.github.io/vlatte/schemas/http.schema.json
request:
  method: GET
  path: /hello
response:
  status: 200
  body: hello world
```

Editors that use the YAML language server read it: VS Code with the [YAML extension](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml), Neovim, Helix, Zed and others. JetBrains IDEs read it too.

To map the schemas by file name instead, without a comment in each file, add them to the settings of the YAML extension in VS Code:

```json
{
  "yaml.schemas": {
    "https://caffeine-projects.github.io/vlatte/schemas/http.schema.json": ["**/_mocks/*mock.yaml", "**/_mocks/*mock.yml"],
    "https://caffeine-projects.github.io/vlatte/schemas/grpc.schema.json": ["**/grpc/*.yaml"]
  }
}
```

## JSON

A JSON mock file can't name its schema in a `$schema` key: mock files take no key they don't know. Map the schemas by file name in your editor instead, such as with `json.schemas` in VS Code:

```json
{
  "json.schemas": [
    {"fileMatch": ["**/_mocks/*mock.json"], "url": "https://caffeine-projects.github.io/vlatte/schemas/http.schema.json"}
  ]
}
```

In JetBrains IDEs, add the address under **Settings › Languages & Frameworks › Schemas and DTDs › JSON Schema Mappings**.
