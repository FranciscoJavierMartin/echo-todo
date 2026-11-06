package httpx

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"

	"echo-todo-2/internal/apperr"
)

// Validator adapts go-playground/validator to echo.Validator and turns failures
// into a 422 apperr with one human-readable message per JSON field.
type Validator struct {
	v *validator.Validate
}

func NewValidator() *Validator {
	v := validator.New(validator.WithRequiredStructEnabled())
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		for _, tag := range []string{"json", "query", "param", "form", "header"} {
			if name, _, _ := strings.Cut(f.Tag.Get(tag), ","); name != "" && name != "-" {
				return name
			}
		}
		return f.Name
	})
	_ = v.RegisterValidation("notblank", func(fl validator.FieldLevel) bool {
		return strings.TrimSpace(fl.Field().String()) != ""
	})
	return &Validator{v: v}
}

func (val *Validator) Validate(i any) error {
	return val.ValidateCtx(context.Background(), i)
}

// ValidateCtx is picked up by (*echo.Context).ValidateCtx (echo v5.4.1+).
func (val *Validator) ValidateCtx(ctx context.Context, i any) error {
	err := val.v.StructCtx(ctx, i)
	if err == nil {
		return nil
	}
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return apperr.Internal("httpx.Validator", err)
	}
	fields := make(map[string]string, len(verrs))
	for _, fe := range verrs {
		key := fieldPath(fe)
		if _, exists := fields[key]; !exists {
			fields[key] = message(fe)
		}
	}
	return apperr.InvalidFields(fields).Wrap(err)
}

// fieldPath strips the root struct name: "CreateTodoRequest.title" → "title".
func fieldPath(fe validator.FieldError) string {
	ns := fe.Namespace()
	if _, rest, ok := strings.Cut(ns, "."); ok {
		return rest
	}
	return fe.Field()
}

func message(fe validator.FieldError) string {
	isString := fe.Kind() == reflect.String
	switch fe.Tag() {
	case "required", "notblank":
		return "is required"
	case "max":
		if isString {
			return fmt.Sprintf("must be at most %s characters", fe.Param())
		}
		if fe.Kind() == reflect.Slice {
			return fmt.Sprintf("must contain at most %s items", fe.Param())
		}
		return fmt.Sprintf("must be at most %s", fe.Param())
	case "min":
		if isString {
			return fmt.Sprintf("must be at least %s characters", fe.Param())
		}
		return fmt.Sprintf("must be at least %s", fe.Param())
	case "oneof":
		return "must be one of: " + strings.ReplaceAll(fe.Param(), " ", ", ")
	case "email":
		return "must be a valid email address"
	case "uuid", "uuid4":
		return "must be a valid UUID"
	case "gte":
		return "must be greater than or equal to " + fe.Param()
	case "lte":
		return "must be less than or equal to " + fe.Param()
	default:
		return "is invalid (" + fe.Tag() + ")"
	}
}
