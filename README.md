# cloudmap proxy

This is a tiny network proxy written in Go that can resolve target address with AWS CloudMap service discovery service.

No Route 53 integration is necessary.

## Synopsis

```
cloudmap-proxy -cache-ttl 60s -conn-timeout 10s -debug aws-servicediscovery:namespace-name:service-name:8080 :8080
```

## Command-line spec

```
cloudmap-proxy [-cache-ttl TTL] [-conn-timeout TTL] [-debug] TARGET_ADDR LISTEN_ADDR
```

* `-cache-ttl`

    Specify cache duration. (default: 1m0s)  The value must followed by a duration unit.

* `-conn-timeout`

    Specify connection timeout. (default: 10s)  The value must followed by a duration unit.

* `-debug`

    Turn on debug mode

* TARGET_ADDR

    The following formats are accepted:

    * `HOST:PORT`

        HOST may be bracketed IPv6 form (`[::1]`)

    * `aws-servicediscovery:namespaceName:serviceName`

        Do the discovery for service `serviceName` in the namespace `namespaceName`.

    * `aws-servicediscovery-v4:namespaceName:serviceName`

        Do the discovery for service `serviceName` in the namespace `namespaceName`, with the preference for IPv4 .

    * `aws-servicediscovery-v6:namespaceName:serviceName`

        Do the discovery for service `serviceName` in the namespace `namespaceName`, with the preference for IPv6.

* LISTEN_ADDR

    The following formats are accepted:

    * `HOST:PORT`

    * `:PORT`
```
