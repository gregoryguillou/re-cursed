package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	opentracing "github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	olog "github.com/opentracing/opentracing-go/log"
	jaeger "github.com/uber/jaeger-client-go"
	config "github.com/uber/jaeger-client-go/config"
	jaegerlog "github.com/uber/jaeger-client-go/log"
	"github.com/uber/jaeger-client-go/zipkin"
	metrics "github.com/uber/jaeger-lib/metrics"
)

var (
	logHeaders bool
	port       string
	remote     string
	istio      bool
)

// Value is the payload that is used to exchange data
type Value struct {
	Value int64 `json:"value"`
}

// RequestHeader can be used to get X-Request-Id from request in Istio
type RequestHeader string

// Init is used to intiantiate the opentracing tracer
func Init(service string) (closer io.Closer) {
	cfg := config.Configuration{
		Sampler: &config.SamplerConfig{
			Type:  jaeger.SamplerTypeConst,
			Param: 1,
		},
		Reporter: &config.ReporterConfig{
			LogSpans:           true,
			LocalAgentHostPort: "jaeger:6831",
		},
	}
	var err error
	if !istio {
		closer, err = cfg.InitGlobalTracer(
			service,
			config.Logger(jaegerlog.StdLogger),
			config.Metrics(metrics.NullFactory),
		)
		if err != nil {
			log.Fatalf("Could not initialize Jaeger tracer: %s", err.Error())
		}
		return
	}

	zipkinPropagator := zipkin.NewZipkinB3HTTPHeaderPropagator()
	closer, err = cfg.InitGlobalTracer(
		service,
		config.Logger(jaegerlog.StdLogger),
		config.Metrics(metrics.NullFactory),
		config.Injector(opentracing.HTTPHeaders, zipkinPropagator),
		config.Extractor(opentracing.HTTPHeaders, zipkinPropagator),
		config.ZipkinSharedRPCSpan(true),
		config.Reporter(jaeger.NewNullReporter()),
	)
	if err != nil {
		log.Fatalf("Could not initialize Zipkin tracer: %s", err.Error())
	}
	return
}

func injectSpan(ctx context.Context, req *http.Request) (span opentracing.Span) {
	if istio && ctx.Value(RequestHeader("x-request-id")) != nil {
		requestID := ctx.Value(RequestHeader("x-request-id")).(string)
		req.Header.Set("x-request-id", requestID)
	}
	span = opentracing.SpanFromContext(ctx)
	ext.SpanKindRPCClient.Set(span)
	ext.HTTPUrl.Set(span, req.URL.String())
	ext.HTTPMethod.Set(span, req.Method)
	span.Tracer().Inject(
		span.Context(),
		opentracing.HTTPHeaders,
		opentracing.HTTPHeadersCarrier(req.Header),
	)
	return
}

// call perform a remote call for values other than 1
func call(ctx context.Context, i int64) int64 {
	input := &Value{
		Value: i - 1,
	}
	var output Value
	buf, _ := json.Marshal(input)
	r := bytes.NewReader(buf)
	req, err := http.NewRequest("POST", remote, r)
	req.Header.Set("Content-Type", "application/json")
	if err != nil {
		panic(err.Error())
	}
	span := injectSpan(ctx, req)
	span.SetTag("execute-for", i)
	span.LogFields(
		olog.String("event", "call-start"),
		olog.String("logs", fmt.Sprintf("function call executed with %d", i)),
	)
	if i == 7 {
		time.Sleep(2 * time.Second)
	}
	client := http.Client{}
	if resp, err := client.Do(req); err == nil {
		if body, err := ioutil.ReadAll(resp.Body); err == nil {
			if err := json.Unmarshal(body, &output); err == nil {
				span.LogFields(
					olog.String("event", "call-end"),
					olog.String(
						"logs",
						fmt.Sprintf(
							"function previous call returned %d",
							output.Value,
						),
					),
				)
				return output.Value
			}
		}
	}
	return -1
}

func extractSpan(r *http.Request) (span opentracing.Span) {
	tracer := opentracing.GlobalTracer()
	spanCtx, _ := tracer.Extract(
		opentracing.HTTPHeaders,
		opentracing.HTTPHeadersCarrier(r.Header),
	)
	span = tracer.StartSpan("/root", ext.RPCServerOption(spanCtx))
	return
}

func middlewareCaptureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if logHeaders {
			fmt.Println("Headers:")
			for k, v := range r.Header {
				fmt.Printf("%q: %q\n", k, v)
			}
			fmt.Println("--------")
		}
		next.ServeHTTP(w, r)
	})
}

// recurse is the handler that manages the application only route
func recurse(w http.ResponseWriter, r *http.Request) {
	span := extractSpan(r)
	defer span.Finish()
	var input Value
	output := &Value{
		Value: 0,
	}
	if body, err := ioutil.ReadAll(r.Body); err == nil {
		json.Unmarshal(body, &input)
		output.Value = 1
		if input.Value > 1 {
			ctx := opentracing.ContextWithSpan(context.Background(), span)
			requestID := r.Header.Get("x-request-id")
			if istio && requestID != "" {
				ctx = context.WithValue(ctx, RequestHeader("x-request-id"), requestID)
			}
			output.Value = input.Value + call(ctx, input.Value)
		}
		result, _ := json.Marshal(output)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "%s\n", result)
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
}

func main() {

	flag.StringVar(
		&port,
		"port",
		"8000",
		"The default port for the application",
	)
	flag.StringVar(
		&remote,
		"remote",
		"http://localhost:8000",
		"The remote service location exposed on the outside",
	)
	flag.BoolVar(&istio,
		"istio",
		false,
		"Set Istio Envoy-based tracing, including Zipkin headers",
	)
	flag.BoolVar(&logHeaders,
		"log-headers",
		false,
		"Display headers as part of the service logs",
	)
	flag.Parse()

	closer := Init("recursed")
	defer closer.Close()

	r := mux.NewRouter()
	r.Handle("/", middlewareCaptureHeaders(
		handlers.LoggingHandler(
			os.Stdout,
			http.HandlerFunc(recurse),
		)))

	srv := &http.Server{
		Handler:      r,
		Addr:         "0.0.0.0:8000",
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
	}
	log.Printf("Starting on %s\n", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}
