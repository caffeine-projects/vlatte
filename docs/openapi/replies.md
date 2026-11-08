# Replies

Each response an operation declares becomes a mock, one per media type. This page uses a small spec:

```yaml
openapi: 3.1.0
info:
  title: Bookshop
  version: 1.0.0
paths:
  /books/{isbn}:
    get:
      operationId: getBook
      parameters:
        - name: isbn
          in: path
          required: true
          schema:
            type: string
            pattern: '^[0-9]{13}$'
          examples:
            dune:
              value: "9780441013593"
      responses:
        "200":
          description: The book
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Book"
              example:
                isbn: "9780441013593"
                title: Dune
                pages: 412
            text/plain:
              schema:
                type: string
              example: Dune, 412 pages
        "404":
          description: No such book
        "503":
          description: Try again later
components:
  schemas:
    Book:
      type: object
      required: [isbn, title]
      properties:
        isbn:
          type: string
        title:
          type: string
        pages:
          type: integer
          minimum: 1
```

## Bodies

A body is the spec's example for its media type: `example`, or the first of `examples`. Each other named example gets a mock of its own, its name at the end of the file name, written disabled: [turn it on](#which-mocks-are-on) when a test needs it. Without an example, it is a value made up to fit the schema, with plausible words, dates, emails and IDs:

```sh
curl http://127.0.0.1:8080/pets/3fa85f64-5717-4562-b3fc-2c963f66afa6
# {"ageMonths":30,"breed":"meadow cedar","color":"amber","createdAt":"2023-06-29T17:34:29Z",...}
```

Made-up values are the same on every run, so tests can assert on them, and a rerun leaves the files unchanged.

An example that doesn't fit its schema is converted when its text reads as the type the schema asks, such as `12345` for a string, and left out otherwise. The command logs a warning for each.

## Path parameter examples

When every path parameter of an operation has examples, its success mock answers only those values, and its `404` answers the others:

```sh
curl http://127.0.0.1:8080/books/9780441013593
# {"isbn":"9780441013593","pages":412,"title":"Dune"}
curl -i http://127.0.0.1:8080/books/9780000000000
# HTTP/1.1 404 Not Found
```

Without examples, or without a `404` response, the success mock answers any value.

## Content negotiation

Each media type of a response gets a mock, which answers the client whose `Accept` header prefers it. A client that sends no `Accept` gets the first media type of the spec. A client that takes none of them gets a `406`, which lists them:

```sh
curl -H 'Accept: text/plain' http://127.0.0.1:8080/books/9780441013593
# Dune, 412 pages
curl -H 'Accept: application/xml' http://127.0.0.1:8080/books/9780441013593
# expected one of: application/json, text/plain
```

## Bodies that aren't JSON

Media types other than JSON take their body as text, as the example writes it. A string schema in `base64` format sends the bytes it encodes. XML, and any other text format, needs a string example: an object has no text form, so a mock without one is written disabled.

In OpenAPI 3.2, give such a media type a `serializedValue`, which is sent as is. A `dataValue` there is left out, as the mocks don't serialize data to a format other than JSON. JSON bodies take `value`, or else `dataValue`.

## Which mocks are on

The mocks a request decides between are on: the success reply of each operation, its `404` when path examples ask for one, the `406`, and the mocks that [refuse requests that break the spec](/openapi/contract).

The mocks of responses nothing in a request predicts, such as a `503`, a second success status or a second named example, are written disabled. Turn one on to see how a client copes, with the [admin API](/admin-api/):

```sh
curl -X PATCH -H 'Content-Type: application/json' -d '{"enabled": true}' \
  http://127.0.0.1:8081/mocks/books-_isbn-get-503
curl -i http://127.0.0.1:8080/books/9780441013593
# HTTP/1.1 503 Service Unavailable
```

and `{"enabled": false}` turns it off again. A mock's ID is its file name without `.mock.yaml`.

## Changing a reply

To answer differently from what the spec gives, write a mock file by hand, in the same folder or another the server loads. Hand-written mocks win over generated ones: a mock file without a `priority` answers before any generated mock that also matches.

```yaml
# testdata/vlatte/_mocks/dune-out-of-print.mock.yaml
request:
  method: GET
  path: /books/9780441013593
response:
  status: 410
```

```sh
curl -i http://127.0.0.1:8080/books/9780441013593
# HTTP/1.1 410 Gone
```

Every other request still gets the generated replies, and a rerun of `vlatte openapi` leaves this file alone.
