package domain

import (
	"fmt"
	"net/http"
)

type AppError struct {
	Code       string      `json:"code"`
	Message    string      `json:"message"`
	Details    interface{} `json:"details,omitempty"`
	HTTPStatus int         `json:"-"`
	Err        error       `json:"-"`
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error {
	return e.Err
}

func NewAppError(code string, message string, httpStatus int) *AppError {
	return &AppError{Code: code, Message: message, HTTPStatus: httpStatus}
}

func (e *AppError) WithDetails(details interface{}) *AppError {
	e.Details = details
	return e
}

func (e *AppError) WithErr(err error) *AppError {
	e.Err = err
	return e
}

func ErrUnauthorized(msg string) *AppError {
	return NewAppError("ERR_UNAUTHORIZED", msg, http.StatusUnauthorized)
}

func ErrForbidden(msg string) *AppError {
	return NewAppError("ERR_FORBIDDEN", msg, http.StatusForbidden)
}

func ErrNotFound(code, msg string) *AppError {
	return NewAppError(code, msg, http.StatusNotFound)
}

func ErrConflict(code, msg string) *AppError {
	return NewAppError(code, msg, http.StatusConflict)
}

func ErrValidation(msg string) *AppError {
	return NewAppError("ERR_VALIDATION", msg, http.StatusUnprocessableEntity)
}

func ErrInternal(msg string, err error) *AppError {
	return &AppError{
		Code:       "ERR_INTERNAL",
		Message:    msg,
		HTTPStatus: http.StatusInternalServerError,
		Err:        err,
	}
}
