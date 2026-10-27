//go:build dynamic

package main

import (
	"bytes"
	"crypto/x509"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

func TestDynamicKafkaNativeRuntime(t *testing.T) {
	caBundle, err := os.ReadFile("/etc/ssl/certs/ca-certificates.crt")
	if err != nil {
		t.Fatal(err)
	}
	if !x509.NewCertPool().AppendCertsFromPEM(caBundle) {
		t.Fatal("runtime CA bundle contains no parseable certificates")
	}
	if os.Getenv("LOITERING_EXACT_RUNTIME") == "1" {
		if os.Getuid() != 65532 || os.Getgid() != 65532 {
			t.Fatalf("unexpected runtime identity %d:%d", os.Getuid(), os.Getgid())
		}
		if _, err := os.ReadFile("/etc/resolv.conf"); err != nil {
			t.Fatal(err)
		}
	}
	if kafka.LibrdkafkaLinkInfo != "dynamically linked to librdkafka" {
		t.Fatal(kafka.LibrdkafkaLinkInfo)
	}
	_, version := kafka.LibraryVersion()
	if !strings.HasPrefix(version, "2.16.0") {
		t.Fatalf("unexpected packaged librdkafka version %q", version)
	}
	t.Logf("%s: %s", kafka.LibrdkafkaLinkInfo, version)

	cluster, err := kafka.NewMockCluster(1)
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Close()

	const features = "gzip,snappy,ssl,sasl,lz4,sasl_plain,sasl_scram,sasl_oauthbearer,zstd,http,oidc"
	producer, err := kafka.NewProducer(&kafka.ConfigMap{
		"bootstrap.servers":  cluster.BootstrapServers(),
		"security.protocol":  "PLAINTEXT",
		"builtin.features":   features,
		"message.timeout.ms": 5000,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	t.Logf("required native features: %s", features)

	topic := "loitering-native-fixture"
	payload := []byte("dynamic-librdkafka-round-trip")
	delivery := make(chan kafka.Event, 1)
	if err := producer.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{Topic: &topic, Partition: kafka.PartitionAny},
		Value:          payload,
	}, delivery); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-delivery:
		message, ok := event.(*kafka.Message)
		if !ok {
			t.Fatalf("unexpected delivery event %T", event)
		}
		if message.TopicPartition.Error != nil {
			t.Fatal(message.TopicPartition.Error)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("mock Kafka delivery timed out")
	}

	consumer, err := kafka.NewConsumer(&kafka.ConfigMap{
		"bootstrap.servers":  cluster.BootstrapServers(),
		"security.protocol":  "PLAINTEXT",
		"group.id":           "loitering-native-fixture",
		"auto.offset.reset":  "earliest",
		"enable.auto.commit": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	if err := consumer.SubscribeTopics([]string{topic}, nil); err != nil {
		t.Fatal(err)
	}
	message, err := consumer.ReadMessage(15 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(message.Value, payload) {
		t.Fatalf("received %q, want %q", message.Value, payload)
	}
}
