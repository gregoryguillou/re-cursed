package main

import (
	"bytes"
	"encoding/json"
	"context"
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
	jaeger "github.com/uber/jaeger-client-go"
	config "github.com/uber/jaeger-client-go/config"
)

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

type Value struct {
	Value int64 `json:"value"`
}

func call(req *http.Request, ctx context.Context, i int64) int64 {
	span, _ := opentracing.StartSpanFromContext(ctx, "call")
    defer span.Finish()
	input := &Value{
		Value: i - 1,
	}
	var output Value
	buf, _ := json.Marshal(input)
	r := bytes.NewReader(buf)
	ext.SpanKindRPCClient.Set(span)
	ext.HTTPUrl.Set(span, remote)
	ext.HTTPMethod.Set(span, "POST")
	span.Tracer().Inject(
		span.Context(),
		opentracing.HTTPHeaders,
		opentracing.HTTPHeadersCarrier(req.Header),
	)
	if resp, err := http.Post(remote, "application/json", r); err == nil {
		if body, err := ioutil.ReadAll(resp.Body); err == nil {
			if err := json.Unmarshal(body, &output); err == nil {
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
    var input Value
	output := &Value{
		Value: 0,
	}
	if body, err := ioutil.ReadAll(r.Body); err == nil {
		json.Unmarshal(body, &input)
		output.Value = 1
		if input.Value > 1 {
			output.Value = input.Value + call(r, context.Background(), input.Value)
		}
		result, _ := json.Marshal(output)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "%s\n", result)
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
}

var (
	port   string
	remote string
)

func main() {

	flag.StringVar(&port, "port", "8000", "The default port for the application")
	flag.StringVar(&remote, "remote", "http://localhost:8000", "The remote service location exposed on the outside")
	flag.Parse()

	r := mux.NewRouter()
	r.HandleFunc("/", recurse)

	tracer, closer := Init("re-curse")
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
