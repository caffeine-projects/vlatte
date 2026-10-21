local header = import './http_header.libsonnet';
local mediaTypes = import './http_media_types.libsonnet';
local method = import './http_method.libsonnet';
local statuses = import './http_status.libsonnet';
local internal = import './internal/internal.libsonnet';
local scheme = import './scheme.libsonnet';

(scheme) +
(method) +
(header) +
(mediaTypes) +
(statuses) +

{
  mock(v):: internal.merge(v),

  id(id):: { id: id },
  name(n):: { name: n },
  priority(p):: { priority: p },
  enabled(e=true):: { enabled: e },

  delay(duration):: { delay: duration },
  repeat(r):: { [if !std.isNumber(r) then error 'repeat must be a number' else 'repeat']: r },
  scenario(name, requiredState='', newState=''):: { scenario: { name: name, [if std.isString(requiredState) && !std.isEmpty(requiredState) then 'required_state']: requiredState, [if std.isString(newState) && !std.isEmpty(newState) then 'new_state']: newState } },

  request(fields):: { request: internal.merge(fields) },
  req: {
    scheme(matcher):: { scheme: matcher },
    isHTTP():: self.scheme(scheme.SchemeHTTP),
    isHTTPS():: self.scheme(scheme.SchemeHTTPS),

    method(v):: { method: if std.isString(v) then std.asciiUpper(v) else v },
    get():: self.method(method.MethodGet),
    head():: self.method(method.MethodHead),
    post():: self.method(method.MethodPost),
    put():: self.method(method.MethodPut),
    patch():: self.method(method.MethodPatch),
    delete():: self.method(method.MethodDelete),
    options():: self.method(method.MethodOptions),
    trace():: self.method(method.MethodTrace),

    host(matcher):: { host: matcher },
    clientIP(matcher):: { client_ip: matcher },

    path(matcher):: { path: matcher },
    url(matcher):: { url: matcher },
    urlMatch(matcher):: { url_match: matcher },
    pathTemplate(template):: { path_template: template },
    pathParam(name, matcher):: { path_params: { [name]: matcher } },
    fragment(matcher):: { fragment: matcher },
    header(key, matcher):: { header: { [key]: matcher } },
    headers(key, matcher):: { headers: { [key]: matcher } },
    cookie(name, matcher):: { cookie: { [name]: matcher } },
    basicAuth(username, password):: { basic_auth: { username: username, password: password } },
    query(key, matcher):: { query: { [key]: matcher } },
    queries(key, matcher):: { queries: { [key]: matcher } },
    form(key, matcher):: { form: { [key]: matcher } },
    forms(key, matcher):: { forms: { [key]: matcher } },
    formFile(field, pattern):: { form_file: { [field]: pattern } },
    body(matcher):: { body: matcher },
  },

  response(v):: { response: internal.merge(v) },
  res: {
    status(code):: { status: code },
    ok():: self.status(statuses.StatusOK),

    header(map):: { header: map },
    headerTemplate(map):: { header_template: map },
    body(b):: { body: b },
    bodyFile(f):: { body_file: f },
    multipart(spec):: { multipart: spec },
    encoding(e):: { encoding: e },
    gzip():: self.encoding('gzip'),

    sequence(list):: { response_sequence: internal.merge(list) },
    seq: {
      afterEnded(reply): { after_ended: reply },
    },
  },

  postAction(name, parameters=null):: { name: name, [if std.type(parameters) != 'null' then 'parameters']: parameters },
  webhook(parameters):: self.postAction('webhook', parameters),
  postActions(actions):: { [if !std.isArray(actions) then error 'postActions expects an array of post actions' else 'on_complete']: actions },

  ext(v):: { ext: v },
}
