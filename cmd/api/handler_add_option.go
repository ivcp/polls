package main

import (
	"net/http"

	"github.com/ivcp/polls/internal/data"
	"github.com/ivcp/polls/internal/validator"
)

func (app *application) addOptionHandler(w http.ResponseWriter, r *http.Request) {
	poll := r.Context().Value("poll").(*data.Poll)

	var input struct {
		Value    string `json:"value"`
		Position int    `json:"position"`
	}

	err := app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	newOption := &data.PollOption{
		Value:    input.Value,
		Position: input.Position,
	}

	poll.Options = append(poll.Options, newOption)

	v := validator.New()

	if data.ValidatePoll(v, poll); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	err = app.models.PollOptions.Insert(newOption, poll.ID)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	err = app.writeJSON(w, http.StatusCreated, envelope{"message": "option added successfully"}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
