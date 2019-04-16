# livelog

[![logo](https://media.taskcluster.net/logo/logo-96x120.png)](https://media.taskcluster.net/logo/logo-96x120.png)

[![Build Status](https://travis-ci.org/taskcluster/livelog.svg?branch=master)](http://travis-ci.org/taskcluster/livelog)
[![GoDoc](https://godoc.org/github.com/taskcluster/livelog?status.svg)](https://godoc.org/github.com/taskcluster/livelog)
[![Coverage Status](https://coveralls.io/repos/taskcluster/livelog/badge.svg?branch=master&service=github)](https://coveralls.io/github/taskcluster/livelog?branch=master)
[![License](https://img.shields.io/badge/license-MPL%202.0-orange.svg)](http://mozilla.org/MPL/2.0)

## TODO
  - [x] byte range fetching
  - [x] aggregate pending writes into single buffer and/or multiple write
    then flush when no longer writing.
  - [ ] cli
  - [x] cross domain headers
  - [x] http api

Livelog is a service that enables both secure and insecure streaming of binary
content (typically log files) over HTTP(S).

It achieves this by exposing an interface for receiving log data via an HTTP
PUT request (typically on tcp port 60022), and exposing a separate interface
for reading the log via HTTP GET typically on port 60023.

It is written in go, which compiles to a native binary for most conceivable
platforms, and can therefore be deployed almost anywhere.

Multiple clients can concurrently access the GET interface, also specifying
HTTP RANGE headers, while only a single client can PUT data. Furthermore, the
log file content must be served to livelog with a single (long-lived) PUT
request. The GET url is only available after the connection to the PUT
interface has been initiated.

## URLs

When used with default ports:

* PUT: http://localhost:60022/log
* GET: http(s)://localhost:60023/log/`${ACCESS_TOKEN}`

To alter the port numbers, set environment variables `LIVELOG_PUT_PORT` and/or
`LIVELOG_GET_PORT` to the preferred values when starting the livelog server.
For example, in bash:

```
export LIVELOG_PUT_PORT=32815
export LIVELOG_GET_PORT=32844
```

`ACCESS_TOKEN` is an arbitrary url-safe string that you provide via the
`ACCESS_TOKEN` environment variable to the livelog process when it starts up.
The provides some level or security via obscurity when managed as a secret
between client and server, especially when used in combination with https.

By default http is used for serving GET requests, unless environment variables
`SERVER_CRT_FILE` and `SERVER_KEY_FILE` are set, in which case these should
specify the file location of suitable SSL certificate and key to be used for
https transport. This will cause the GET interface to be served over https.

**Note**, the PUT interface is always http, i.e. not secured. Therefore this
port should only be opened on the loopback interface (localhost) in order that
log content cannot be published from a malicious host over the network!

## Binary packages
See the [github releases](https://github.com/taskcluster/livelog/releases) page.

## Example Usage

### Example 1 - insecure over http

Terminal 1: Start service

```
ACCESS_TOKEN='secretpuppy' DEBUG='*' livelog
```

Terminal 2: Pump data into the PUT interface

```
(for ((i=1; i<=500; i++)); do echo "Log line $i"; sleep 1; done) | curl -v -T - http://localhost:60022/log
```

Terminal 3: Read from GET interface

```
curl http://localhost:60023/log/secretpuppy
```

### Example 2 - secure over https, using non-default ports

For this example, **you'll need a valid SSL key and certificate** for some
domain.

Let's say, your SSL key/certificate are for domain `taskcluster-worker.net`,
and you have them on your filesystem at locations `~/myssl.crt` and
`~/myssl.key` in PEM format (`-----BEGIN` ...).

First make sure a name under *your domain* (in this example, we'll use
`pete.taskcluster-worker.net`) resolves to your local machine:

```
echo '127.0.0.1 pete.taskcluster-worker.net' | sudo tee -a /etc/hosts >/dev/null
```

Note, if you wish to, you can also run this example over two machines
(client/server). In that case, you would run this on the client machine, and
replace `127.0.0.1`, with the IP address of the server (or just have real DNS
records). The SSL certificate and key are obviously only needed on the server.

Terminal 1: Start service (server)

```
ACCESS_TOKEN='secretpuppy' DEBUG='*' LIVELOG_PUT_PORT='34253' LIVELOG_GET_PORT='23536' SERVER_CRT_FILE=~/myssl.crt SERVER_KEY_FILE=~/myssl.key livelog
```

Terminal 2: Pump data into the PUT interface (server)

```
(for ((i=1; i<=500; i++)); do echo "Log line $i"; sleep 1; done) | curl -v -T - http://localhost:34253/log
```

Terminal 3: Read from GET interface (client)

```
curl https://pete.taskcluster-worker.net:23536/log/secretpuppy
```

## Performance

Under heavy load while memory does not massively explode it does spike
and due to how go returns memory to the OS the memory will stay at that
level for up to 5 minutes. The best case situation is a low number of
clients (or no clients) who stream from beginning to end [this should be
uncommon]. For the more likely case (burst usage) the server is fairly
aggressive about closing connections with large amounts of pending data.
This means that the server can handle massive load and deliver some
amount of writes but drop other clients as needed in a mostly
first-come-first-server fashion (really also depends on how fast clients
can read from the socket).

## Tests

Tests are written in Node.JS largely because I wanted to write some
quickly and node has a great/easy interface to do the nonblocking
http stuff I wanted.

Usage:
```sh
npm install
npm test
```

## Configuration
The following environment variables can be used to configure the server.

 * `ACCESS_TOKEN` secret access token required for access (**required**)
 * `SERVER_CRT_FILE` path to SSL certificate file (optional)
 * `SERVER_KEY_FILE` path to SSL private key file (optional)
 * `DEBUG` set to '*' to see debug logs (optional)
 * `LIVELOG_PUT_PORT` PUT port number (optional - default is 60022)
 * `LIVELOG_GET_PORT` GET port number (optional - default is 60023)
