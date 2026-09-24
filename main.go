package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	input := flag.String("input", "", "process a local workflow invocation JSON file without RabbitMQ or HTTP")
	once := flag.Bool("once", false, "exit after one delivery (preview always exits after one)")
	flag.Parse()
	settings, err := loadConfig(*input != "")
	if err != nil {
		return err
	}
	if *input != "" {
		body, err := os.ReadFile(*input)
		if err != nil {
			return err
		}
		result, _, err := handleInvocation(body, settings.operation, settings.start, settings.duration)
		if err != nil {
			return err
		}
		return printPreview(os.Stdout, result)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return consume(ctx, settings, *once)
}

func handleDelivery(ctx context.Context, settings config, delivery amqp.Delivery, output io.Writer) error {
	result, callbackURL, err := handleInvocation(delivery.Body, settings.operation, settings.start, settings.duration)
	if err == nil {
		if settings.mode == "http" {
			err = postResult(ctx, settings, callbackURL, result)
		} else {
			err = printPreview(output, result)
		}
	}
	if err != nil || settings.mode == "preview" {
		if nackErr := delivery.Nack(false, true); nackErr != nil {
			return errors.New("could not requeue delivery; closing connection")
		}
		return err
	}
	return delivery.Ack(false)
}

func consume(ctx context.Context, settings config, once bool) error {
	connection, err := amqp.DialConfig(settings.brokerURL, amqp.Config{Dial: amqp.DefaultDial(10 * time.Second)})
	if err != nil {
		return errors.New("RabbitMQ connection failed; check host, TLS, vhost and credentials")
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		return errors.New("cannot open RabbitMQ channel")
	}
	defer channel.Close()
	if err := channel.Qos(1, 0, false); err != nil {
		return errors.New("cannot set RabbitMQ prefetch")
	}
	deliveries, err := channel.Consume(settings.queue, "", false, false, false, false, nil)
	if err != nil {
		return errors.New("cannot consume STAGE_QUEUE; confirm it exists in this vhost and the account has read permission")
	}
	log.Printf("waiting for a workflow invocation (mode=%s); Ctrl+C to stop", settings.mode)
	for {
		select {
		case <-ctx.Done():
			return nil
		case delivery, open := <-deliveries:
			if !open {
				return errors.New("RabbitMQ delivery stream closed; restart the worker to reconnect")
			}
			if err := handleDelivery(ctx, settings, delivery, os.Stdout); err != nil {
				return err
			}
			if settings.mode == "preview" {
				log.Print("preview only: message requeued, no result submitted to Hub")
				return nil
			}
			if settings.mode == "bypass" {
				log.Print("bypass: message processed and acknowledged, no result submitted to Hub")
			} else {
				log.Print("result accepted by callback; delivery acknowledged")
			}
			if once {
				return nil
			}
		}
	}
}
