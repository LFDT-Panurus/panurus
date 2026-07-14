# Extending a Validator Driver

This guide explains how to extend an existing token validator driver with custom validation functions. 
This is useful when you need to enforce additional business rules or compliance checks beyond the default logic provided by the token drivers (e.g., `FabToken` or `ZKAT-DLog`).

## Overview

Panurus uses a `ValidatorDriverService` to manage factories for creating `driver.Validator` instances. 
Each driver version is identified by a unique string (e.g., `zkatdlognogh.v1`).

To extend a validator, you typically:
1.  Implement a custom `driver.ValidatorDriver` that wraps an existing one.
2.  Override the `NewValidator` method to inject additional validation logic.
3.  Register your custom driver factory in Panurus's dependency injection container.

## Architecture

The `ValidatorDriverService` (found in `token/core/service.go`) maintains a map of driver identifiers to `driver.ValidatorDriver` implementations.

```go
type ValidatorDriverService struct {
	*factoryDirectory[driver.ValidatorDriver]
}

func (s *ValidatorDriverService) NewValidator(pp driver.PublicParameters) (driver.Validator, error) {
	if driver, ok := s.factories[DriverIdentifierFromPP(pp)]; ok {
		return driver.NewValidator(pp)
	}
	return nil, errors.Errorf("no validator found for token driver [%s]", DriverIdentifierFromPP(pp))
}
```

By providing a custom factory with the same identifier as an existing driver, you can effectively "hijack" the validator creation process.

## Example: Extending the ZKAT-DLog Validator

Suppose you want to add a custom check to all transfer operations in a `ZKAT-DLog` system.

### 1. Define your custom validation function

First, define a function that matches the signature expected by the validator. For `ZKAT-DLog` (NOGH v1), this is `ValidateTransferFunc`.

```go
package myextension

import (
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/driver"
)

func MyCustomTransferValidation(ctx validator.Context, tr *transfer.Action) error {
	// Perform your custom validation logic here.
	// For example, check if the transfer metadata contains a specific attribute.
	if len(tr.Metadata) == 0 {
		return errors.New("transfer metadata is missing")
	}
	return nil
}
```

### 2. Create a custom Validator Driver

Implement the `driver.ValidatorDriver` interface by wrapping the standard one.

```go
type MyValidatorDriver struct {
	driver.ValidatorDriver // Wrap the existing driver
}

func (d *MyValidatorDriver) NewValidator(pp driver.PublicParameters) (driver.Validator, error) {
	// We can't easily use the wrapped driver's NewValidator if we want to 
    // inject functions into its internal pipeline, so we replicate its logic.
    
	ppp, ok := pp.(*v1.PublicParams)
	if !ok {
		return nil, errors.Errorf("invalid public parameters type [%T]", pp)
	}
    
	deserializer, err := driver.NewDeserializer(ppp) // Assume driver is the zkatdlog driver package
	if err != nil {
		return nil, err
	}
    
	logger := logging.DriverLoggerFromPP("panurus.driver.myextension", string(pp.TokenDriverName()))

	// Instantiate the validator with your custom function
	return validator.New(
		logger,
		ppp,
		deserializer,
		[]validator.ValidateTransferFunc{MyCustomTransferValidation}, // Extra transfer validators
		nil, // Extra issuer validators
		nil, // Extra auditor validators
	), nil
}
```

### 3. Register the extension

Register your custom factory using Panurus's registration mechanism. If you are using the `dig` container (standard in FSC-based applications), you can provide it to the `token-validator-drivers` group.

```go
func NewMyValidatorDriver() core.NamedFactory[driver.ValidatorDriver] {
	return core.NamedFactory[driver.ValidatorDriver]{
		Name:   core.DriverIdentifier(v1.DLogNoGHDriverName, v1.ProtocolV1),
		Driver: &MyValidatorDriver{
            // You might need to initialize the wrapped driver here
        },
	}
}
```

By using the same `Name` as the original driver, the `ValidatorDriverService` will use your factory instead of the default one.

## Alternative: Generic Validator Wrapping

If you want to add validation that is independent of the driver's internal implementation, you can wrap the `driver.Validator` interface directly.

```go
type WrappedValidator struct {
	driver.Validator
}

func (v *WrappedValidator) VerifyTokenRequestFromRaw(ctx context.Context, getState driver.GetStateFnc, anchor driver.TokenRequestAnchor, raw []byte) ([]interface{}, driver.ValidationAttributes, error) {
	// Call the original validator first
	actions, attrs, err := v.Validator.VerifyTokenRequestFromRaw(ctx, getState, anchor, raw)
	if err != nil {
		return nil, nil, err
	}
    
	// Perform post-validation
	for _, action := range actions {
		if err := myGlobalCheck(action); err != nil {
			return nil, nil, err
		}
	}
    
	return actions, attrs, nil
}
```

This approach is highly portable and works across all token drivers.
