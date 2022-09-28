package main

import (
	"bytes"
	"encoding/base64"
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
	MAX_UPLOAD_SIZE = 4096 * 1024
)

var config struct {
	API    `group:"HTTP Server Options" namespace:"http" env-namespace:"HTTP"`
	Tidbyt `group:"Tidbyt Options" namespace:"tidbyt" env-namespace:"TIDBYT"`
	Debug  		bool `long:"debug-mode" env:"DEBUG_MODE" description:"Debug Mode"`	
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

// notify : parameters for notify router
type notify struct {
	Text            string `json:"text"`      // Text to send in notification
	TextColor       string `json:"textcolor"` // Text Color to set. Default: White
	BackgroundColor string `json:"bgcolor"`   // Background color to set: Default: Black
	TextSize        int    `json:"textsize"`  // Test font size to set. Default: 14
	Icon            string `json:"icon"`      // Icon to send in notification by name
}

// image : parameters for image router
type image struct {
	Source          string `json:"-"` 	    // Provided Image base64 encoded 
	BackgroundColor string `json:"bgcolor"` // Background color to set: Default: Black
	Height          int    `json:"height"`  // Image Height. Default: 32
	Width           int    `json:"width"`   // Image Width. Default: 64
	Delay           int    `json:"delay"`   // Image Render Delay. Default: 0
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
	r.HandleFunc("/api/image", imageHandler)
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
		if v.TextColor == "" {
			v.TextColor = "#ffffff"
		}
		if v.TextSize == 0 {
			v.TextSize = 14
		}
		if v.BackgroundColor == "" {
			v.BackgroundColor = "#000000"
		}
	case image:
		if v.Height == 0 {
			v.Height = 32
		}
		if v.Width == 0 {
			v.Width = 64
		}
	}
}

// notifyHandler: send simple text notification to tidbyt device
func notifyHandler(w http.ResponseWriter, r *http.Request) {
	log.Tracef("notifyHandler:\nrequest:\n%+v", r)

	// load all star templates
	availableTemplates = template.Must(template.ParseGlob(templatePath))

	var notify notify
	timestamp := time.Now().Unix()

	err := json.NewDecoder(r.Body).Decode(&notify)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// set sane defaults for unset parameters
	parameterDefaults(notify)

	// create temporary template file
	templateFile, tmplErr := ioutil.TempFile(config.ScratchDirectory, "tidbyt*.star")
	if tmplErr != nil {
		log.Fatal(tmplErr)
		return
	}

	// render from template
	renderErr := availableTemplates.ExecuteTemplate(templateFile, "notify", notify)
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

	// if debug: true, return the result in the response
	if config.Debug {
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
	}

	// cleanup template/render files
	defer os.Remove(templateFile.Name())
	defer os.Remove(outputFile)
}

// imageHandler: send images to tidbyt device
func imageHandler(w http.ResponseWriter, r *http.Request) {
	log.Tracef("imageHandler:\nrequest:\n%+v", r)

	var image image
	timestamp := time.Now().Unix()

	err := json.NewDecoder(r.Body).Decode(&image)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// set sane defaults for unset parameters
	parameterDefaults(image)

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MAX_UPLOAD_SIZE)
	if err := r.ParseMultipartForm(MAX_UPLOAD_SIZE); err != nil {
		http.Error(w, "The uploaded file is too big. Please choose an file that's less than 5MB in size", http.StatusBadRequest)
		return
	}
	img, _, err := r.FormFile("image")
	defer img.Close()
	if err != nil {
		log.Print(err.Error())
		return
	}
	buff := &bytes.Buffer{}
	_, buffErr := buff.ReadFrom(img)
	if err != nil {
		log.Print(buffErr.Error())
		return
	}	
	image.Source = base64.StdEncoding.EncodeToString(buff.Bytes())

	// create temporary template file
	templateFile, tmplErr := ioutil.TempFile(config.ScratchDirectory, "tidbyt*.star")
	if tmplErr != nil {
		log.Fatal(tmplErr)
		return
	}

	// render from template
	renderErr := availableTemplates.ExecuteTemplate(templateFile, "image", image)
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

	// if debug: true, return the result in the response
	if config.Debug {
		w.Header().Set("Content-Type", "image/jpeg")
		io.Copy(w, img)
	}
	// push rendered webp to target device if provided
	if config.ApiKey != "" && config.DeviceID != "" {
		pushOutput, err := exec.Command(pixletBinary, "push", "--api-token", config.ApiKey, config.DeviceID, outputFile).CombinedOutput()
		log.Debugf("%s", string(pushOutput))
		if err != nil {
			log.Println(err.Error())
			return
		}
	}

	// cleanup template/render files
	defer os.Remove(templateFile.Name())
	defer os.Remove(outputFile)
}
