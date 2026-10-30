; net/http 入口：http.HandleFunc("/path", handler) / mux.Handle("/path", h)
(call_expression
  function: (selector_expression
    operand: (identifier) @http.receiver
    field: (field_identifier) @http.method)
  arguments: (argument_list) @http.args
  (#match? @http.method "^(HandleFunc|Handle)$")) @http.route
