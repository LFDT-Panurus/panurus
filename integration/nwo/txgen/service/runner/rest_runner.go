/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package runner

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/logging"

	"github.com/gin-gonic/gin"
	"go.yaml.in/yaml/v3"
)

// RestRunner enhances the BaseRunner by adding a REST server to accept new suites
type RestRunner struct {
	SuiteRunner
	logger  logging.Logger
	address string
}

func NewRest(runner SuiteRunner, config model.ServerConfig, logger logging.Logger) *RestRunner {
	return &RestRunner{
		logger:      logger,
		SuiteRunner: runner,
		address:     config.Endpoint,
	}
}

func (s *RestRunner) Start(ctx context.Context) error {
	if err := s.SuiteRunner.Start(ctx); err != nil {
		return err
	}

	if len(s.address) == 0 {
		s.logger.Infof("No endpoint passed. No remote server starting.")

		return nil
	}
	fmt.Println("starting server")
	s.logger.Infof("Start remote suite listener on %s/suites\n", s.address)
	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()

	router.POST("/suites", func(c *gin.Context) {
		s.logger.Infof("Received new suite request.")

		var request struct {
			Suites []model.SuiteConfig `json:"suites" yaml:"suites"`
		}
		if body, err := io.ReadAll(c.Request.Body); err != nil {
			s.logger.Errorf("Error reading body: %s", err)
			c.IndentedJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		} else if err := yaml.Unmarshal(body, &request); err != nil {
			s.logger.Errorf("Error parsing body [%s]: %s", string(body), err)
			c.IndentedJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		} else {
			s.logger.Infof("New request: %v. Pushing %d suites to the stack.", request.Suites, len(request.Suites))
			s.PushSuites(request.Suites...)
			s.logger.Infof("Pushed %d suites to the stack.", len(request.Suites))
			c.IndentedJSON(http.StatusOK, request)
		}
	})
	go func() {
		s.logger.Infof("Listening on %s/suites for new suites", s.address)
		err := router.Run(s.address)
		if err != nil {
			s.logger.Errorf("Error running remote controller: %s", err)
		}
	}()

	return nil
}
