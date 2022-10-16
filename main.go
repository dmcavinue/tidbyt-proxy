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

	"github.com/eclipse/paho.mqtt.golang"
	"github.com/gorilla/mux"
	flags "github.com/jessevdk/go-flags"
	log "github.com/sirupsen/logrus"
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

type Image struct {
	Image           string `json:"image"`   // Image url
	BackgroundColor string `json:"bgcolor"` // Background color to set: Default: Black
	Height          int    `json:"height"`  // Test font size to set. Default: 32
	Width           int    `json:"width"`   // Test font size to set. Default: 64
	CommonOptions
}

type Notify struct {
	Text            string `json:"text"`      // Text to send in notification
	TextColor       string `json:"textcolor"` // Text Color to set. Default: White
	BackgroundColor string `json:"bgcolor"`   // Background color to set: Default: Black
	TextSize        int    `json:"textsize"`  // Test font size to set. Default: 14
	Icon            string `json:"icon"`      // Icon to send in notification by name
	CommonOptions
}

// CommonOptions : Common Pixlet/Development Options
type CommonOptions struct {
	ReturnImage    bool   `json:"return_image"`
	InstallationID string `json:"installation_id"`
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
	case Notify:
		if v.TextColor == "" {
			v.TextColor = "#fff"
		}
		if v.TextSize == 0 {
			v.TextSize = 14
		}
		if v.BackgroundColor == "" {
			v.BackgroundColor = "#000"
		}
	case Image:
		if v.Height == 0 {
			v.Height = 32
		}
		if v.Width == 0 {
			v.Width = 64
		}
		if v.BackgroundColor == "" {
			v.BackgroundColor = "#000"
		}
	}
}

// convert []byte to base64 string
func toBase64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

// publishToMQTT: publish to MQTT Topic
func publishToMQTT(imgFile string) error {
	var imgBase64 string
	// Read the entire file into a byte slice
	imgBytes, err := ioutil.ReadFile(imgFile)
	if err != nil {
		log.Println(err.Error())
		return err
	}
	imgBase64 += toBase64(imgBytes)

	m := MQTTOptions{
		Applet: Applet{
			Applet:  "tidbyt-proxy",
			Payload: imgBase64,
		},
	}
	payload, err := json.Marshal(m.Applet)
	if err != nil {
		log.Println(err)
		return err
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
	return nil
}

// imageHandler: send an image to tidbyt device
func imageHandler(w http.ResponseWriter, r *http.Request) {
	log.Tracef("imageHandler:\nrequest:\n%+v", r)

	// load all star templates
	availableTemplates = template.Must(template.ParseGlob(templatePath))

	var image Image
	timestamp := time.Now().Unix()

	err := json.NewDecoder(r.Body).Decode(&image)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// set sane defaults for unset parameters
	parameterDefaults(image)

	// create temporary template file
	templateFile, tmplErr := ioutil.TempFile(config.API.ScratchDirectory, "tidbyt*.star")
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

	// if returnimage: true, return the result in the response
	if image.ReturnImage {
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
		pixletArgs := []string{
			"push",
			"--api-token", config.Tidbyt.ApiKey,
			config.Tidbyt.DeviceID,
			outputFile,
		}
		// install the pushed app if InstallationID is provided
		if image.InstallationID != "" {
			pixletArgs = append(pixletArgs, "--installation-id", image.InstallationID)
		}

		pushOutput, err := exec.Command(pixletBinary, pixletArgs...).CombinedOutput()
		log.Debugf("%s", string(pushOutput))
		if err != nil {
			log.Println(err.Error())
			return
		}
	}
	// send to target MQTT topic if provided
	if config.MQTT.Host != "" {
		err := publishToMQTT(outputFile)
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

// notifyHandler: send simple text/icon notification to tidbyt device
func notifyHandler(w http.ResponseWriter, r *http.Request) {
	log.Tracef("notifyHandler:\nrequest:\n%+v", r)

	// load all star templates
	availableTemplates = template.Must(template.ParseGlob(templatePath))

	var notify Notify
	timestamp := time.Now().Unix()

	err := json.NewDecoder(r.Body).Decode(&notify)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// set sane defaults for unset parameters
	parameterDefaults(notify)

	// create temporary template file
	templateFile, tmplErr := ioutil.TempFile(config.API.ScratchDirectory, "tidbyt*.star")
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

	// if returnimage: true, return the result in the response
	if notify.ReturnImage {
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
		pixletArgs := []string{
			"push",
			"--api-token", config.Tidbyt.ApiKey,
			config.Tidbyt.DeviceID,
			outputFile,
		}
		// install the pushed app if InstallationID is provided
		if notify.InstallationID != "" {
			pixletArgs = append(pixletArgs, "--installation-id", notify.InstallationID)
		}

		pushOutput, err := exec.Command(pixletBinary, pixletArgs...).CombinedOutput()
		log.Debugf("%s", string(pushOutput))
		if err != nil {
			log.Println(err.Error())
			return
		}
	}
	// send to target MQTT topic if provided
	if config.MQTT.Host != "" {
		err := publishToMQTT(outputFile)
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
