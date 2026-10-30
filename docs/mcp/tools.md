# Tools

Each tool runs an [admin API](/admin-api/) operation. A tool's arguments carry what its route takes, the body under a key, such as `{"mock": ...}` for `add_mock` and `{"query": ...}` for `query_requests`, and its schema names them. The server lists only the tools it can do: the last column says when a tool is there. A server without a journal has none of the request tools, and a gRPC server has no `mock_schema`.

## Server

| Tool | Route | What it does | Listed when |
|---|---|---|---|
| `health` | `GET /health` | Tells the server's kind, such as `http`, and the URL it serves its mocks at. | Always |
| `reset` | `POST /reset` | Removes the mocks the admin API added, loads the mock files again, starts every scenario over and clears the journal. Mocks added in code, and the key-value store, stay. | Always |

## Mocks

| Tool | Route | What it does | Listed when |
|---|---|---|---|
| `list_mocks` | `GET /mocks` | Lists every mock, enabled or not, in the order mocks match requests, with its priority, hits and origin. | Always |
| `get_mock` | `GET /mocks/{id}` | Gets a mock as a mock file describes it, which `replace_mock` takes back. | Always |
| `mock_schema` | `GET /schema` | Gets the JSON Schema of the server's mock files. | HTTP servers |
| `validate_mock` | `POST /mocks/validate` | Checks a mock as `add_mock` would, without adding it, and lists its problems. | Always |
| `add_mock` | `POST /mocks` | Adds a mock, which the server serves at once. An id in use is refused. | Always |
| `replace_mock` | `PUT /mocks/{id}` | Replaces a mock in one step: a request finds the old mock or the new one, never neither. | Always |
| `update_mock` | `PATCH /mocks/{id}` | Enables or disables a mock. A disabled mock matches no request, and keeps its hits. | Always |
| `remove_mock` | `DELETE /mocks/{id}` | Removes a mock. | Always |
| `remove_all_mocks` | `DELETE /mocks` | Removes every mock, of mock files and code too. | Always |
| `reload_mocks` | `POST /mocks/reload` | Loads the mock files, and the mocks the admin API keeps, again. | Always |
| `save_mocks` | `POST /mocks/save` | Writes the mocks the admin API added as mock files, and lists their paths. | Always |

## Recording

| Tool | Route | What it does | Listed when |
|---|---|---|---|
| `start_recording` | `POST /recording` | Starts a recording: requests go to its target, and their answers become mock files once it stops. | Always |
| `recording_status` | `GET /recording` | Counts the recording's calls and the mocks it wrote, or tells that none runs. | Always |
| `stop_recording` | `DELETE /recording` | Stops the recording, and lists the mock files it wrote, which the server serves from then on. | Always |

See [Recording](/recording/cli-admin).

## Scenarios

| Tool | Route | What it does | Listed when |
|---|---|---|---|
| `list_scenarios` | `GET /scenarios` | Lists every scenario: its state, the states its mocks name, and which mock moves it where. | Always |
| `set_scenario` | `PUT /scenarios/{name}` | Moves a scenario to a state. A state none of its mocks names is refused. | Always |
| `reset_scenario` | `DELETE /scenarios/{name}` | Moves one scenario back to where it started: every instance of it, or the one of a key. | Always |
| `reset_scenarios` | `DELETE /scenarios` | Moves every scenario back to where it started. | Always |

See [Scenarios](/scenarios/).

## Key-value store

| Tool | Route | What it does | Listed when |
|---|---|---|---|
| `list_kv` | `GET /kv` | Lists the keys of the key-value store templates read, in order, without their values. | Always |
| `get_kv` | `GET /kv/{key}` | Gets the value of a key. A key that isn't set is refused. | Always |
| `set_kv` | `PUT /kv/{key}` | Sets a key to a value, which templates read from the next request on. | Always |
| `delete_kv` | `DELETE /kv/{key}` | Removes a key. A key that isn't set is no error. | Always |
| `reload_kv` | `POST /kv/reload` | Loads the store again from its file, in place of every value. A file with a problem changes nothing. | The store loads from a file, as the command's `kv.file` does |

See [Key-value store](/templating/#key-value-store).

## Requests

| Tool | Route | What it does | Listed when |
|---|---|---|---|
| `list_requests` | `GET /requests` | Lists the newest requests of the journal, newest first, a page at a time. | The server keeps a journal |
| `get_request` | `GET /requests/{id}` | Gets a request, with its answer and the mock that answered it. | The server keeps a journal |
| `query_requests` | `QUERY /requests` | Finds and counts the requests a query selects, such as those a mock's request part matches. | The server keeps a journal |
| `verify_requests` | `QUERY /requests/verify` | Checks that the journal holds the requests a test expects: how many, by when, or in what order. A failed check says why. | The server keeps a journal |
| `clear_requests` | `DELETE /requests` | Removes every request of the journal. | The server keeps a journal |

`verify_requests` takes the check under `check`: see [Verifying over MCP](/verify/admin-api#mcp) and the [request journal](/more/journal).
