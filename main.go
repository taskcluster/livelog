package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/pprof"
	"os"
	"strconv"
	"sync"

	stream "github.com/taskcluster/livelog/writer"
	. "github.com/visionmedia/go-debug"
)

var debug = Debug("livelog")

const (
	DEFAULT_PUT_PORT = 60022
	DEFAULT_GET_PORT = 60023
)

func abort(writer http.ResponseWriter) error {
	// We need to hijack and abort the request...
	conn, _, err := writer.(http.Hijacker).Hijack()

	if err != nil {
		return err
	}

	// Force the connection closed to signal that the response was not
	// completed...
	conn.Close()
	return nil
}

func startLogServe(stream *stream.Stream, getAddr string) {
	// Get access token from environment variable
	accessToken := os.Getenv("ACCESS_TOKEN")

	routes := http.NewServeMux()
	routes.HandleFunc("/log/", func(w http.ResponseWriter, r *http.Request) {
		debug("output %s %s", r.Method, r.URL.String())

		// Authenticate the request with accessToken, this is good enough because
		// live logs are short-lived, we do this by slicing away '/log/' from the
		// URL and comparing the reminder to the accessToken, ensuring a URL pattern
		// /log/<accessToken>
		if r.URL.String()[5:] != accessToken {
			writeHeaders(w, r)
			w.WriteHeader(401)
			fmt.Fprint(w, "Access denied")
		} else if r.Method == "HEAD" {
			writeHeaders(w, r)
			// If we are creating a HEAD request, we can also mark that the subsequent
			// GET request exposes access to X-Streaming
			w.Header().Set("Access-Control-Allow-Headers", "X-Streaming")
			w.WriteHeader(200)
			debug("Sending HEAD request headers")
		} else {
			getLog(stream, w, r)
		}
	})

	server := http.Server{
		Addr:    getAddr,
		Handler: routes,
	}

	crtFile := os.Getenv("SERVER_CRT_FILE")
	keyFile := os.Getenv("SERVER_KEY_FILE")
	if crtFile != "" && keyFile != "" {
		debug("Output server listening... %s (with TLS)", server.Addr)
		debug("key %s ", keyFile)
		debug("crt %s ", crtFile)
		server.ListenAndServeTLS(crtFile, keyFile)
	} else {
		debug("Output server listening... %s (without TLS)", server.Addr)
		server.ListenAndServe()
	}
}

func writeHeaders(
	writer http.ResponseWriter,
	req *http.Request,
) {
	// TODO: Allow the input stream to configure headers rather then assume
	// intentions...
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("X-Streaming", "true")
	writer.Header().Set("Access-Control-Expose-Headers", "X-Streaming")

	log.Printf("%v", req.Header)
}

// HTTP logic for serving the contents of a stream...
func getLog(
	stream *stream.Stream,
	writer http.ResponseWriter,
	req *http.Request,
) {
	rng, rngErr := ParseRange(req.Header)

	if rngErr != nil {
		log.Printf("Invalid range : %s", req.Header.Get("Range"))
		writer.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		writer.Write([]byte(rngErr.Error()))
		return
	}

	handle := stream.Observe(rng.Start, rng.Stop)

	defer func() {
		// Ensure we close our file handle...
		// Ensure the stream is cleaned up after errors, etc...
		stream.Unobserve(handle)
		debug("send connection close...")
	}()

	// Send headers so its clear what we are trying to do...
	writeHeaders(writer, req)
	writer.WriteHeader(200)
	debug("wrote headers...")

	// Begin streaming any pending results...
	_, writeToErr := handle.WriteTo(writer)
	if writeToErr != nil {
		log.Println("Error during write...", writeToErr)
		abort(writer)
	}
}

// Logic here mostly inspired by what docker does...
func attachProfiler(router *http.ServeMux) {
	router.HandleFunc("/debug/pprof/", pprof.Index)
	router.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	router.HandleFunc("/debug/pprof/profile", pprof.Profile)
	router.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	router.HandleFunc("/debug/pprof/heap", pprof.Handler("heap").ServeHTTP)
	router.HandleFunc("/debug/pprof/goroutine", pprof.Handler("goroutine").ServeHTTP)
	router.HandleFunc("/debug/pprof/threadcreate", pprof.Handler("threadcreate").ServeHTTP)
}

func main() {
	// TODO: Right now this is a collection of hacks until we build out something
	// nice which can handle multiple log connections. Right now the intent is to
	// use this as a process per task (which has overhead) but should be fairly
	// clean (memory wise) in the long run as we will terminate the process
	// frequently per task run.

	handlingPut := false
	mutex := sync.Mutex{}

	routes := http.NewServeMux()

	if os.Getenv("DEBUG") != "" {
		attachProfiler(routes)
	}

	// portAddressOrExit is a helper function to translate a port number in an
	// envronment variable into a valid address string which can be used when
	// starting web service. This helper function will cause the go program to
	// exit if an invalid value is specified in the environment variable.
	portAddressOrExit := func(envVar string, defaultValue uint16, notANumberExitCode, outOfRangeExitCode int) (addr string) {
		addr = fmt.Sprintf(":%v", defaultValue)
		if port := os.Getenv(envVar); port != "" {
			p, err := strconv.Atoi(port)
			if err != nil {
				debug("env var %v is not a number (%v)", envVar, port)
				os.Exit(notANumberExitCode)
			}
			if p < 0 || p > 65535 {
				debug("env var %v is not between [0, 65535] (%v)", envVar, p)
				os.Exit(outOfRangeExitCode)
			}
			addr = ":" + port
		}
		return
	}

	putAddr := portAddressOrExit("LIVELOG_PUT_PORT", DEFAULT_PUT_PORT, 64, 65)
	getAddr := portAddressOrExit("LIVELOG_GET_PORT", DEFAULT_GET_PORT, 66, 67)

	server := http.Server{
		// Main put server listens on the public root for the worker.
		Addr:    putAddr,
		Handler: routes,
	}

	// The "main" http server is for the PUT side which should not be exposed
	// publicly but via links in the docker container... In the future we can
	// handle something fancier.
	routes.HandleFunc("/log", func(w http.ResponseWriter, r *http.Request) {
		debug("input %s %s", r.Method, r.URL.String())

		if r.Method != "PUT" {
			debug("input not put")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("This endpoint can only handle PUT requests"))
			return
		}

		// Threadsafe checking of the `handlingPut` flag
		mutex.Lock()
		if handlingPut {
			debug("Attempt to put when in progress")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("This endpoint can only process one http PUT at a time"))
			mutex.Unlock() // used instead of defer so we don't block other rejections
			return
		}
		mutex.Unlock() // So we don't block other rejections...

		stream, streamErr := stream.NewStream(r.Body)

		if streamErr != nil {
			debug("input stream open err", streamErr)
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Could not open stream for body"))

			// Allow for retries of the initial put if something goes wrong...
			mutex.Lock()
			handlingPut = false
			mutex.Unlock()
		}

		// Signal initial success...
		w.WriteHeader(http.StatusCreated)

		// Initialize the sub server in another go routine...
		debug("Begin consuming...")
		go startLogServe(stream, getAddr)
		consumeErr := stream.Consume()
		if consumeErr != nil {
			log.Println("Error finalizing consume of stream", consumeErr)
			abort(w)
			return
		}
	})

	// Listen forever on the PUT side...
	debug("input server listening... %s", server.Addr)
	server.ListenAndServe()
}
