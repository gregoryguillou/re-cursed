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
	"time"

	"github.com/gorilla/mux"
	opentracing "github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	olog "github.com/opentracing/opentracing-go/log"
	jaeger "github.com/uber/jaeger-client-go"
	config "github.com/uber/jaeger-client-go/config"
)

// Init is used to intiantiate the opentracing tracer
func Init(service string) (opentracing.Tracer, io.Closer) {
	cfg := &config.Configuration{
		Sampler: &config.SamplerConfig{
			Type:  "const",
			Param: 1,
		},
		Reporter: &config.ReporterConfig{
			LogSpans: true,
		},
	}
	tracer, closer, err := cfg.New(service, config.Logger(jaeger.StdLogger))
	if err != nil {
		panic(fmt.Sprintf("ERROR: cannot init Jaeger: %v\n", err))
	}
	return tracer, closer
}

// Value is the payload that is used to exchange data
type Value struct {
	Value int64 `json:"value"`
}

var (
	port   string
	remote string
)

func call(ctx context.Context, i int64) int64 {
	span := opentracing.SpanFromContext(ctx)
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
	ext.SpanKindRPCClient.Set(span)
	ext.HTTPUrl.Set(span, remote)
	ext.HTTPMethod.Set(span, "POST")
	span.Tracer().Inject(
		span.Context(),
		opentracing.HTTPHeaders,
		opentracing.HTTPHeadersCarrier(req.Header),
	)
	span.SetTag("execute-for", i)
	span.LogFields(
		olog.String("event", "call-start"),
		olog.String("logs", fmt.Sprintf("function call executed with %d", i)),
	)
	client := http.Client{}
	if resp, err := client.Do(req); err == nil {
		if body, err := ioutil.ReadAll(resp.Body); err == nil {
			if err := json.Unmarshal(body, &output); err == nil {
				span.LogFields(
					olog.String("event", "call-end"),
					olog.String("logs", fmt.Sprintf("function previous call returned %d", output.Value)),
				)
				return output.Value
			}
		}
	}
	return -1
}

func recurse(w http.ResponseWriter, r *http.Request) {
	tracer := opentracing.GlobalTracer()
	spanCtx, _ := tracer.Extract(opentracing.HTTPHeaders, opentracing.HTTPHeadersCarrier(r.Header))
	span := tracer.StartSpan("recurse", ext.RPCServerOption(spanCtx))
	defer span.Finish()
	ctx := opentracing.ContextWithSpan(context.Background(), span)
	var input Value
	output := &Value{
		Value: 0,
	}
	if body, err := ioutil.ReadAll(r.Body); err == nil {
		json.Unmarshal(body, &input)
		output.Value = 1
		if input.Value > 1 {
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

	flag.StringVar(&port, "port", "8000", "The default port for the application")
	flag.StringVar(&remote, "remote", "http://localhost:8000", "The remote service location exposed on the outside")
	flag.Parse()

	r := mux.NewRouter()
	r.HandleFunc("/", recurse)

	tracer, closer := Init("recurse")
	defer closer.Close()
	opentracing.SetGlobalTracer(tracer)

	srv := &http.Server{
		Handler:      r,
		Addr:         "0.0.0.0:8000",
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
	}

	log.Fatal(srv.ListenAndServe())

}
