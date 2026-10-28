---
layout: home

hero:
  name: '<span class="vl-v">v</span>latte'
  text: API mocking for Go
  tagline: Mock HTTP and gRPC servers for Go tests. Match requests, reply with templates, record and proxy. A CLI that serves HTTP and gRPC mocks from files.
  image:
    src: /logo.svg
    alt: ''
  actions:
    - theme: brand
      text: Quick start
      link: /getting-started/quick-start
    - theme: alt
      text: What is vlatte?
      link: /getting-started/
    - theme: alt
      text: GitHub
      link: https://github.com/caffeine-projects/vlatte

features:
  - title: HTTP and gRPC
    details: Real servers in your tests. Mock any gRPC method of any call type, unary to bidi, with no service implementation.
    link: /getting-started/concepts
  - title: Rich matchers
    details: Match on method, path, headers, query, forms, files and body, with JSONPath, XPath, JSON Schema, dates and more.
    link: /features/matchers
  - title: Templated replies
    details: Render headers and bodies from the request with Go templates and a library of JSON, string, time and hash functions.
    link: /features/templates
  - title: Record and proxy
    details: Forward to real services, record what passes through as mock files, and play it back without the service.
    link: /http/recording
  - title: Faults and steps
    details: Test how clients cope with slow networks, dribbled bodies, resets, garbage and timeouts.
    link: /http/faults
  - title: CLI and admin API
    details: Serve mock files with the vlatte command or Docker, and change mocks at run time over a REST API or MCP.
    link: /run/cli
---
