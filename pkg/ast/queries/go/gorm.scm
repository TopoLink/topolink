; GORM 数据库调用：db.Where(...), db.Find(...), db.Create(...) 等
(call_expression
  function: (selector_expression
    field: (field_identifier) @db.method)
  arguments: (argument_list) @db.args
  (#match? @db.method "^(Where|Find|First|Take|Last|Create|Save|Update|Updates|Delete|Raw|Exec|Table|Model|Select|Joins|Preload|Scan|Count|Pluck|Omit|Unscoped)$")) @db.call
