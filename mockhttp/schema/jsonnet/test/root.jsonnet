local vlatte = import '../http.libsonnet';
local m = import '../matchers.libsonnet';

local req = vlatte.req;
local res = vlatte.res;

vlatte.mock([
  vlatte.repeat(10),
  vlatte.scenario('scenario_test'),
  vlatte.request([
    req.scheme('http'),
    req.method('post'),
    req.host('api.local'),
    req.clientIP(m.eq('127.0.0.1')),
    req.pathTemplate('/users/{id}'),
    req.pathParam('id', m.eq('7')),
    req.header('hello', m.eq('world')),
    req.header('content-type', 'application/json'),
    req.header('test', m.present()),
    req.header('complex', m.all([m.eq('test'), m.present()])),
    req.headers('accept', ['some', m.eq('text/html')]),
    req.cookie('session', 'abc'),
    req.basicAuth('jeff', m.eq('s3cret')),
    req.query('filter', m.both(m.eq('test'), m.present())),
    req.form('title', m.eq('Holiday')),
    req.forms('tag', ['len', 2]),
    req.formFile('photo', { filename: m.eq('a.png'), content_type: 'image/png' }),
    req.custom([['body-too-long', 2048], 'is-admin']),
  ]),
  vlatte.response([
    res.ok(),
    res.multipart({ type: 'mixed', boundary: 'b', parts: [{ content_type: 'text/plain', body: 'saved' }] }),
  ]),
  vlatte.postActions([
    vlatte.postAction('pa', { name: 'test' }),
    vlatte.postAction('no_args'),
    vlatte.postAction('basic', 'dev'),
    vlatte.webhook({ url: 'http://localhost:8080/hook', method: 'POST', body: 'saved', delay: '1s' }),
  ]),
])
