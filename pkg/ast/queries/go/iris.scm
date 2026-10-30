; Iris HTTP 路由入口：app.Get("/path"[, middleware...], handler)（方法名首字母大写）
(call_expression
  function: (selector_expression
    operand: (identifier) @http.receiver
    field: (field_identifier) @http.method)
  arguments: (argument_list) @http.args
  (#match? @http.method "^(Get|Post|Put|Delete|Patch|Any|Head|Options)$")) @http.route
