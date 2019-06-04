# recursed

A recursing microservice to demonstrate OpenTracing with one-only
service.

## How to use it

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


