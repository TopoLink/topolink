; Kafka 消费者入口：sarama ConsumerGroupHandler 的 ConsumeClaim / ConsumePartition
(method_declaration
  name: (_) @kafka.method
  (#match? @kafka.method "^(ConsumeClaim|ConsumePartition|Consume)$")) @kafka.consumer
