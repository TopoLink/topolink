; go-chi/chi 路由入口：r.Get("/path", handler)（方法名首字母大写）
(call_expression
  function: (selector_expression
    operand: (identifier) @http.receiver
    field: (field_identifier) @http.method)
  arguments: (argument_list) @http.args
  (#match? @http.method "^(Get|Post|Put|Delete|Patch|Head|Options|Connect|Trace)$")) @http.route
