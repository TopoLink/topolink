; gRPC 服务注册：pb.RegisterOrderServer(s, &orderServer{})
(call_expression
  function: (selector_expression
    field: (field_identifier) @grpc.register)
  arguments: (argument_list
    (_) @grpc.server
    (_) @grpc.impl)
  (#match? @grpc.register "^Register\\w+Server$")) @grpc.route
