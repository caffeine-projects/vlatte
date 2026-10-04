local vlatte = import 'http.libsonnet';
local m = import 'matchers.libsonnet';

local req = vlatte.req;

vlatte.mock([
  vlatte.repeat(10),
  vlatte.scenario('scenario_test'),
  vlatte.request([
    req.isHTTP(),
    req.method('post'),
    req.header('hello', m.eq('world')),
    req.header('content-type', 'application/json'),
    req.header('test', m.present()),
    req.header('complex', m.all([m.eq('test'), m.present()])),
    req.query('filter', m.both(m.eq('test'), m.present())),
  ]),
  vlatte.postActions([vlatte.postAction('pa', { name: 'test' }), vlatte.postAction('no_args'), vlatte.postAction('basic', 'dev')]),
])
