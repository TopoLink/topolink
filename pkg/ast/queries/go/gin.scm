; Gin HTTP 路由入口：r.GET("/path"[, middleware...], handler)
(call_expression
  function: (selector_expression
    operand: (identifier) @http.receiver
    field: (field_identifier) @http.method)
  arguments: (argument_list) @http.args
  (#match? @http.method "^(GET|POST|PUT|DELETE|PATCH|Any|HEAD|OPTIONS)$")) @http.route
