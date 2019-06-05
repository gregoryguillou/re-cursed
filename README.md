# recursed

A recursing microservice to demonstrate OpenTracing with one-only
service.

## Using recursed with jaegertracing/all-in-one

This demonstration is based on a simple `docker-compose.yaml` file. The
associated resources are all available in the project directory and on
docker hub
[gregoryguillou/recursed](https://cloud.docker.com/u/gregoryguillou/repository/docker/gregoryguillou/recursed).

For a quick start, you can download the `docker-compose.yml` file and
run `docker-compose up`. You should be able:

- To run a test with `curl` and the command below:

```shell
curl -v 0.0.0.0:8000/ -d '{"value": 8}' \
  -XPOST -H 'Content-Type: application/json'
```

- To access UI on this [url](http://localhost:16686/)

![Jaeger UI](jaeger-ui.png)

If you want to know more, visit
[the associated article on my blog](https://gregoryguillou.github.io/2019-06/hint-opentracing)

## Using recursed with Istio and the Jaeger Envoy-Tracing

The `-istio` flag should allow to use `recursed` with the Envoy-based tracing that
relies on B3/Zipkin headers and on the `x-request-id` header. This remains to be
properly tested and documented. Do not hesitate to open issues.

A few related and interested issues or documents:

- [How to use a centrally-deployed Jaeger installation](https://github.com/istio/istio/issues/11340)
- [Using an external jaeger installation](https://github.com/istio/istio/issues/14274)
- [apache/incubator-zipkin-b3-propagation](https://github.com/apache/incubator-zipkin-b3-propagation)
- [Support customized tags from request header for tracing](https://github.com/istio/istio/issues/13018)
