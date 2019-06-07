package main

import (
	"os"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"time"

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
	debug  bool
	port   string
	remote string
	istio  bool
)

// Value is the payload that is used to exchange data
type Value struct {
	Value int64 `json:"value"`
}

// Init is used to intiantiate the opentracing tracer
func Init(service string) (closer io.Closer) {
	cfg := config.Configuration{
		Sampler: &config.SamplerConfig{
			Type:  jaeger.SamplerTypeConst,
			Param: 1,
		},
	}
	agentPort := os.Getenv("JAEGER_AGENT_PORT")
	if agentPort == "" { agentPort = "6831" }
	agentHost := os.Getenv("JAEGER_AGENT_HOST")
	if agentHost == "" { agentHost = "localhost" }
    log.Printf("Configuring for istio: %t...", istio)
	if !istio {
		cfg.Reporter = &config.ReporterConfig{
			LogSpans:           true,
			LocalAgentHostPort: fmt.Sprintf("%s:%s", agentHost, agentPort),
		}
	} else {
		cfg.Reporter = &config.ReporterConfig{
			LogSpans:           true,
		}
	}
	jLogger := jaegerlog.StdLogger
	jMetricsFactory := metrics.NullFactory
	var err error
	if !istio {
		closer, err = cfg.InitGlobalTracer(
			service,
			config.Logger(jLogger),
			config.Metrics(jMetricsFactory),
		)
		if err != nil {
			log.Printf("Could not initialize Jaeger tracer: %s", err.Error())
			return nil
		}
	} else {
		zipkinPropagator := zipkin.NewZipkinB3HTTPHeaderPropagator()
		closer, err = cfg.InitGlobalTracer(
			service,
			config.Logger(jLogger),
			config.Metrics(jMetricsFactory),
			config.Injector(opentracing.HTTPHeaders, zipkinPropagator),
			config.Extractor(opentracing.HTTPHeaders, zipkinPropagator),
			config.ZipkinSharedRPCSpan(true),
		)
		if err != nil {
			log.Printf("Could not initialize zipkin tracer: %s", err.Error())
			return nil
		}
	}
	return
}

func injectSpan(ctx context.Context, req *http.Request) (span opentracing.Span) {
	span = opentracing.SpanFromContext(ctx)
	ext.SpanKindRPCClient.Set(span)
	ext.HTTPUrl.Set(span, req.URL.String())
	ext.HTTPMethod.Set(span, req.Method)
	span.Tracer().Inject(
		span.Context(),
		opentracing.HTTPHeaders,
		opentracing.HTTPHeadersCarrier(req.Header),
	)
	if istio && span.BaggageItem("x-request-id") != "" {
		req.Header.Set("x-request-id", span.BaggageItem("x-request-id"))
	}
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
	if istio && r.Header.Get("x-request-id") != "" {
		span.SetBaggageItem("x-request-id", r.Header.Get("x-request-id"))
	}
	return
}

func middlewareCaptureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if debug {
			fmt.Println("--------")
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
	flag.BoolVar(&debug,
		"debug",
		false,
		"Display headers as part of the service logs",
	)
	flag.Parse()

	closer := Init("recursed")
	defer closer.Close()

	r := mux.NewRouter()
	r.Handle("/", middlewareCaptureHeaders(http.HandlerFunc(recurse)))

	srv := &http.Server{
		Handler:      r,
		Addr:         "0.0.0.0:8000",
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
	}

	log.Fatal(srv.ListenAndServe())
}
