package main

import (
	"crypto/tls"
	b64 "encoding/base64"
	"encoding/json"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strconv"
	"time"

	"github.com/hubert-heijkers/tm1-blackhawk/utils"
	"github.com/joho/godotenv"
)

// Environment variables
var tm1ServiceRootURL string
var interval int

// The http client, extended with some odata functions, we'll use throughout.
var client *odata.Client

// Some variables we use for this specific sample implemenation
var threadMap map[int]time.Time
var queryCount int
var lastQuery time.Time

// processMessageLogEntries is called every time the server has returned a response to either the
// initial or any follow up delta requests. This function then unmarshals the JSON in the resonse
// and iterates any message log entries contained within it.
// This function 'processes' the entries one by one, in the same order as they were injected into
// the message log of the server. Within one run of the server you will never miss any new entries
// nor get any entry more then once for processing.
// Examples of what one could do here are:
//  - Filter and/or store the entries in whatever shape or form in a file or database
//  - Track the time it takes to execute an MDX query (the actual implementation of this sample)
//  - Identify any specific pattern you'd be interested in and have the code notify you perhaps?
func processTransactionLogEntries(stream io.Reader) (string, string) {
	reviver := odata.NewJSONReviver(stream)

	outputPipe, outputStream := io.Pipe()

	go func() {
		encoder := json.NewEncoder(outputStream)
		outputStream.Write([]byte("{ \"value\": [ "))
		isFirst := true

		if err := reviver.ParseTransactionLogs(func(txnLogEntry *odata.TransactionLogEntry, done bool) {
			if txnLogEntry != nil {
				if isFirst {
					isFirst = false
				} else {
					outputStream.Write([]byte(", "))
				}
				// TransactionLog is JSON encoded here
				// json.Compact() can be used to convert json to a more compact version here.
				err := encoder.Encode(txnLogEntry)
				if err != nil {
					log.Fatal(err.Error())
				}
			}

			if done {
				outputStream.Write([]byte("] "))
				outputStream.Close()
			}

		}); err != nil {
			log.Fatal(err.Error())
		}
	}()

	// Send a streaming POST request to a target server.
	// OutputPipe is read in a streaming fashion as data is written to the outputStream.
	client.ExecutePOSTRequest("http://localhost:12345", "application/json", outputPipe)

	// // Interate over the message log entries retrieved from the server
	// for _, entry := range res.MessageLogEntries {

	// 	// This is where the action is! This sample implementation is only interested in MDX
	// 	// queries that are being processed by the server. This implementation keeps track of
	// 	// the begin and end times of the MDXViewCreate and dumps those time stamps, including
	// 	// the duration (time it took to create the view) into comma separated output which
	// 	// can be redirected to a file for further analysis.
	// 	if entry.Logger == "TM1.MdxViewCreate" {

	// 		// Create a map, if not done so already, to keep track of MDX views that are being
	// 		// created and map the Thread ID to the start time
	// 		if threadMap == nil {
	// 			threadMap = make(map[int]time.Time)
	// 		}

	// 		// Lookup this thread in the thread map
	// 		tsStart, rec := threadMap[entry.ThreadID]

	// 		// Parse the time stamp for this entry
	// 		tsEntry, _ := time.Parse(time.RFC3339Nano, entry.TimeStamp)

	// 		// Is this the entry indicating that a new view was created?
	// 		if entry.Message == "View is created." {
	// 			// It is, increate the query count
	// 			queryCount++
	// 			// Presumably we recorded the start time as well...
	// 			if rec == true {
	// 				// We did, dump query count, start and end times as well as the duration to output
	// 				fmt.Printf("QUERY,%d,%s,%s,%0.3f\n", queryCount, tsStart.Format(time.RFC3339Nano), tsEntry.Format(time.RFC3339Nano), tsEntry.Sub(tsStart).Seconds())
	// 				delete(threadMap, entry.ThreadID)
	// 			} else {
	// 				fmt.Printf("ERROR,%d,ERROR,ERROR,0.000\n", queryCount)
	// 			}
	// 		} else {
	// 			// Not created so this is the message telling us which MDX we are about to create a view for
	// 			if rec == false {
	// 				threadMap[entry.ThreadID] = tsEntry
	// 			} else {
	// 				fmt.Printf("ERROR,%d,VIEW CREATED EXPECTED,ERROR,0.000\n", queryCount)
	// 			}
	// 		}
	// 	}
	// }

	// Return the nextLink and deltaLink, if there any
	// return res.NextLink, res.DeltaLink
	return "", ""
}

func main() {
	// Load environment variables from .env file
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}
	tm1ServiceRootURL = os.Getenv("TM1_SERVICE_ROOT_URL")
	interval, _ = strconv.Atoi(os.Getenv("TM1_TRACKER_INTERVAL"))
	if interval < 1 {
		interval = 5
	}

	// Turn 'Verbose' mode off
	odata.Verbose = false

	// Create the one and only http client we'll be using, with a cookie jar enabled to keep reusing our session
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client = odata.NewClient(http.Client{Transport: tr}, processTransactionLogEntries)
	cookieJar, _ := cookiejar.New(nil)
	client.Jar = cookieJar

	// Validate that the TM1 server is accessable by requesting the version of the server
	req, _ := http.NewRequest("GET", tm1ServiceRootURL+"Configuration/ProductVersion/$value", nil)

	// Since this is our initial request we'll have to provide credentials to be able to authenticate.
	// We support Basic and CAM authentication modes in this example. The authentication mode used is
	// defined by the TM1_AUTHENTICATION environment variable and, if specified, needs to be either
	// "TM1", to use standard TM1 authentication, or "CAM" to use CAM. If no value is specified it
	// defaults to attempting Basic authentication.
	// Note: One could get fancy and issue a request against the server and respond to a 401 by checking
	// the WWW-Authorization header to find out what security is supported by the server if one wanted.
	switch os.Getenv("TM1_AUTHENTICATION") {
	case "CAM":
		// Add the Authorization header triggering the CAM authentication
		cred := b64.StdEncoding.EncodeToString([]byte(os.Getenv("TM1_USER") + ":" + os.Getenv("TM1_PASSWORD") + ":" + os.Getenv("TM1_CAM_NAMESPACE")))
		req.Header.Add("Authorization", "CAMNamespace "+cred)

	case "TM1":
		fallthrough

	default:
		// TM1 authentication maps to basic HTTP authentication, set accordingly
		req.SetBasicAuth(os.Getenv("TM1_USER"), os.Getenv("TM1_PASSWORD"))
	}

	// We'll expect text back in this case but we'll simply dump the content out and won't do any
	// content type verification here
	req.Header.Add("Accept", "*/*")

	// Let's execute the request
	resp, err := client.Do(req)
	if err != nil {
		// Execution of the request failed, log the error and terminate
		log.Fatal(err)
	}

	// Validate that the request executed successfully
	odata.ValidateStatusCode(resp, 200, func() string {
		return "Server responded with an unexpected result while asking for its version number."
	})

	// The body simply contains the version number of the server
	version, _ := ioutil.ReadAll(resp.Body)
	resp.Body.Close()

	// We need at least version 10.2.20500 (read: 10.2.2 FP5) to implement a tracker as it takes
	// advantage of Deltas, using the track-changes preference, implemented in that version for
	// both message log and transaction logs.
	if string(version)[0:10] < "10.2.20500" {
		log.Fatalln("The TM1 Server version of your server is:", string(version), "\n Minimal required version to use a tracker is 10.2.2 FP5!")
	}

	// Track the collection of transaction log entries. This will query the existing entries and
	// then cause the server to query the delta of the collection (read: just the changes) after
	// a defined duration.
	client.TrackCollection(tm1ServiceRootURL, "TransactionLogEntries", time.Duration(interval)*time.Second)
}
