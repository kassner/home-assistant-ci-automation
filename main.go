package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

const emptyCommitHash = "0000000000000000000000000000000000000000"

type webhookPayload struct {
	Ref        string `json:"ref"`
	Before     string `json:"before"`
	After      string `json:"after"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

var hassApiKey string
var hassApiUrl string
var hassEntityId string

func main() {
	hassApiKey = os.Getenv("HASS_API_KEY")
	if hassApiKey == "" {
		panic("Missing env HASS_API_KEY")
	}

	hassApiUrl = os.Getenv("HASS_API_URL")
	if hassApiUrl == "" {
		panic("Missing env HASS_API_URL")
	}

	hassEntityId = os.Getenv("HASS_ENTITY_ID")
	if hassEntityId == "" {
		panic("Missing env HASS_ENTITY_ID")
	}

	m := http.NewServeMux()
	m.HandleFunc("POST /webhook", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}

		var payload webhookPayload
		err = json.Unmarshal(body, &payload)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}

		if payload.Repository.FullName != "canastra.online/monorepo" {
			log.Printf("Skipping, wrong repo: received %+v\n", payload)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		version, ok := strings.CutPrefix(payload.Ref, "refs/tags/")
		if !ok {
			log.Printf("Skipping, wrong ref: received %+v\n", payload)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if payload.Before == emptyCommitHash && payload.After == emptyCommitHash {
			jsonError(w, "Unexpected payload: commit hashes are empty", http.StatusBadRequest)
			return
		}

		if payload.After == emptyCommitHash {
			log.Printf("Skipping, tag was removed: received %+v", payload)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		isOn, err := hassIsOn(hassEntityId)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}

		if isOn {
			log.Printf("Skipping, isOn: received %+v", payload)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		err = hassTurnOn(hassEntityId)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}

		log.Printf("Waking up CI server for %s:%s\n", payload.Repository.FullName, version)
		w.WriteHeader(http.StatusNoContent)
		return
	})

	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":8099"
	}

	http.ListenAndServe(listenAddr, m)
}

func jsonError(w http.ResponseWriter, msg string, status int) {
	body, err := json.Marshal(map[string]string{"message": msg})
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte{}
	}

	log.Printf("Response %d: %s", status, string(body))
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

type hassResultType struct {
	EntityId string `json:"entity_id"`
	State    string `json:"state"`
}

func hassIsOn(entityId string) (bool, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/states/%s", hassApiUrl, entityId), nil)
	if err != nil {
		return false, err
	}

	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", hassApiKey))
	req.Header.Add("Accept", "application/json")
	req.Header.Add("User-Agent", "hass-ci/0.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	var result hassResultType
	err = json.Unmarshal(body, &result)
	if err != nil {
		return false, err
	}

	if result.EntityId != entityId {
		return false, fmt.Errorf("wrong device ID. Expected %s, got %s", entityId, result.EntityId)
	}

	return result.State == "on", nil
}

func hassTurnOn(entityId string) error {
	reqBody, err := json.Marshal(map[string]string{"entity_id": entityId})
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/services/switch/turn_on", hassApiUrl), bytes.NewReader(reqBody))
	if err != nil {
		return err
	}

	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", hassApiKey))
	req.Header.Add("Accept", "application/json")
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("User-Agent", "hass-ci/0.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var result []hassResultType
	err = json.Unmarshal(body, &result)
	if err != nil {
		return err
	}

	if len(result) != 1 {
		return fmt.Errorf("unexpected result from service")
	}

	if result[0].EntityId != entityId {
		return fmt.Errorf("wrong device ID. Expected %s, got %s", entityId, result[0].EntityId)
	}

	if result[0].State != "on" {
		return fmt.Errorf("expected result for %s: on, got %s", entityId, result[0].State)
	}

	return nil
}
