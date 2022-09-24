package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/gorilla/mux"
	flags "github.com/jessevdk/go-flags"
	log "github.com/sirupsen/logrus"
)

const (
	pixletBinary = "pixlet"
	templatePath = "./templates/*.star"
)

var config struct {
	API    `group:"HTTP Server Options" namespace:"http" env-namespace:"HTTP"`
	Tidbyt `group:"Tidbyt Options" namespace:"tidbyt" env-namespace:"TIDBYT"`
	Debug  bool `long:"debug-mode" env:"DEBUG_MODE" description:"Debug Mode"`
}

var availableTemplates *template.Template

type API struct {
	Host             string `long:"http-ip" env:"HTTP_IP" description:"HTTP Server IP" default:"0.0.0.0"`
	Port             int    `long:"http-port" env:"HTTP_PORT" description:"HTTP Server Port" default:"8080"`
	ScratchDirectory string `long:"scratch-dir" env:"SCRATCH_DIR" description:"Scratch Directory used renders" default:"/tmp"`
}

type Tidbyt struct {
	ApiUrl   string `long:"api-url" env:"API_URL" description:"Tidbyt API Url" default:"api.tidbyt.com"`
	ApiKey   string `long:"api-key" env:"API_KEY" description:"Tidbyt API Key"`
	DeviceID string `long:"device-id" env:"DEVICE_ID" description:"Tidbyt Device ID"`
}

type notify struct {
	Text            string `json:"text"`        // Text to send in notification
	TextColor       string `json:"textcolor"`   // Text Color to set. Default: White
	BackgroundColor string `json:"bgcolor"`     // Background color to set: Default: Black
	TextSize        int    `json:"textsize"`    // Test font size to set. Default: 14
	Icon            string `json:"icon"`        // Icon to send in notification by name
	ReturnImage     bool   `json:"returnimage"` // Return resulting image in response
}

// templates : parameter definitions for all available star templates
type templates struct {
	Notify notify
}

var parser = flags.NewParser(&config, flags.Default)

func init() {
	// Log as JSON instead of the default ASCII formatter.
	log.SetFormatter(&log.JSONFormatter{})

	// Output to stdout instead of the default stderr
	// Can be any io.Writer, see below for File example
	log.SetOutput(os.Stdout)

	// Only log the warning severity or above.
	log.SetLevel(log.WarnLevel)
}

func main() {
	if _, err := parser.Parse(); err != nil {
		fmt.Printf("%+v", err)
		switch flagsErr := err.(type) {
		case flags.ErrorType:
			if flagsErr == flags.ErrHelp {
				os.Exit(0)
			}
			os.Exit(1)
		default:
			os.Exit(1)
		}
	}

	if config.Debug {
		log.SetLevel(log.DebugLevel)
	}

	r := mux.NewRouter()
	r.HandleFunc("/api/notify", notifyHandler)
	r.HandleFunc("/healthcheck", healthcheck)

	fmt.Println("Starting server on port", config.Port)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", config.Port), r); err != nil {
		log.Fatal("Error while starting server:", err)
	}
}

// healthcheck: simple healthcheck
func healthcheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// parameterDefaults: Set parameter defaults
func parameterDefaults(p interface{}) {

	switch v := p.(type) {
	case notify:
		// setting default values if no values present
		if v.TextColor == "" {
			v.TextColor = "#fff"
		}
		if v.TextSize == 0 {
			v.TextSize = 14
		}
		if v.BackgroundColor == "" {
			v.BackgroundColor = "#000"
		}
	}
}

// notifyHandler: send simple text notification to tidbyt device
func notifyHandler(w http.ResponseWriter, r *http.Request) {
	log.Tracef("notifyHandler:\nrequest:\n%+v", r)

	// load all star templates
	availableTemplates = template.Must(template.ParseGlob(templatePath))

	var templates templates
	timestamp := time.Now().Unix()

	err := json.NewDecoder(r.Body).Decode(&templates.Notify)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// set sane defaults for unset parameters
	parameterDefaults(templates.Notify)

	// create temporary template file
	templateFile, tmplErr := ioutil.TempFile(config.ScratchDirectory, "tidbyt*.star")
	if tmplErr != nil {
		log.Fatal(tmplErr)
		return
	}

	// render from template
	renderErr := availableTemplates.ExecuteTemplate(templateFile, "notify", templates.Notify)
	if renderErr != nil {
		log.Print(renderErr)
		return
	}
	log.Debugf("rendered template to path %s", templateFile.Name())

	// render file from star template file and push via pixlet
	if _, err := exec.LookPath(pixletBinary); err != nil {
		log.Debug("pixlet binary doesn't exist")
		return
	}
	outputFile := fmt.Sprintf("%s-%d.gif", templateFile.Name(), timestamp)
	renderOutput, err := exec.Command(pixletBinary, "render", templateFile.Name(), "--output", outputFile, "--gif").CombinedOutput()
	log.Debugf("%s", string(renderOutput))
	if err != nil {
		log.Println(err.Error())
		return
	}

	// if returnimage: true, return the result in the response
	if templates.Notify.ReturnImage {
		w.Header().Set("Content-Type", "image/jpeg")
		img, err := os.Open(outputFile)
		if err != nil {
			log.Println(err.Error())
		} else {
			io.Copy(w, img)
		}
	}
	// push rendered webp to target device if provided
	if config.ApiKey != "" && config.DeviceID != "" {
		pushOutput, err := exec.Command(pixletBinary, "push", "--api-token", config.ApiKey, config.DeviceID, outputFile).CombinedOutput()
		log.Debugf("%s", string(pushOutput))
		if err != nil {
			log.Println(err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)
	}

	// cleanup template/render files
	defer os.Remove(templateFile.Name())
	defer os.Remove(outputFile)
}
