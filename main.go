package main

import (
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
	"github.com/eclipse/paho.mqtt.golang"
)

const (
	pixletBinary = "pixlet"
	templatePath = "./templates/*.star"
)

var config struct {
	API    API    `group:"HTTP Server Options" namespace:"http" env-namespace:"HTTP"`
	MQTT   MQTT   `group:"MQTT Options" namespace:"mqtt" env-namespace:"MQTT"`
	Tidbyt Tidbyt `group:"Tidbyt Options" namespace:"tidbyt" env-namespace:"TIDBYT"`
	Debug  bool   `long:"debug-mode" env:"DEBUG_MODE" description:"Debug Mode"`
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

type MQTT struct {	
	Host     string `long:"host" env:"HOST" description:"MQTT Host"`
	Port     int    `long:"port" env:"PORT" description:"MQTT Port" default:"1883"`
	Username string `long:"username" env:"USERNAME" description:"MQTT Username"`
	Password string `long:"password" env:"PASSWORD" description:"MQTT Password"`
	Topic    string `long:"topic" env:"TOPIC" description:"MQTT Topic" default:"plm"`
}

type MQTTOptions struct {
	Brightness int
	Status     string
    Current    string
	Applet     Applet
}

type Applet struct {
	Applet  string `json:"applet"`
	Payload string `json:"payload"`
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

	fmt.Println("Starting server on port", config.API.Port)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", config.API.Port), r); err != nil {
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

func toBase64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
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
	templateFile, tmplErr := ioutil.TempFile(config.API.ScratchDirectory, "tidbyt*.star")
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
	if config.Tidbyt.ApiKey != "" && config.Tidbyt.DeviceID != "" {
		pushOutput, err := exec.Command(pixletBinary, "push", "--api-token", config.Tidbyt.ApiKey, config.Tidbyt.DeviceID, outputFile).CombinedOutput()
		log.Debugf("%s", string(pushOutput))
		if err != nil {
			log.Println(err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)
	}
	// send to target MQTT topic if provided 
	if config.MQTT.Host != "" {
		var imgBase64 string
		// Read the entire file into a byte slice
		imgBytes, err := ioutil.ReadFile(outputFile)
		if err != nil {			
			log.Println(err.Error())
			return			
		}
		imgBase64 += toBase64(imgBytes)

		m := MQTTOptions{
			Applet: Applet{
		    	Applet: "notify",
		    	Payload: imgBase64,
			},
		}
		payload, err := json.Marshal(m.Applet)
		if err != nil {
			log.Println(err)
			return
		}
		
		opts := mqtt.NewClientOptions().AddBroker(fmt.Sprintf("tcp://%s:%d", config.MQTT.Host, config.MQTT.Port))
		opts.SetClientID("tidbyt-proxy")
		opts.SetUsername(config.MQTT.Username)
		opts.SetPassword(config.MQTT.Password)
		opts.SetKeepAlive(60 * time.Second)
		opts.SetPingTimeout(1 * time.Second)

		c := mqtt.NewClient(opts)
		if token := c.Connect(); token.Wait() && token.Error() != nil {
			panic(token.Error())
		}
		token := c.Publish(fmt.Sprintf("%s/applet", config.MQTT.Topic), 0, false, string(payload))
		token.Wait()
		c.Disconnect(250)
		w.WriteHeader(http.StatusOK)
	}

	// cleanup template/render files
	defer os.Remove(templateFile.Name())
	defer os.Remove(outputFile)
}
