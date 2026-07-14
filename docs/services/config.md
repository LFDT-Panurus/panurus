# Config Service

The **Config Service** manages configuration settings for Panurus. It provides a centralized way to access and manage configuration values for various components of Panurus, including token management services, drivers, and internal services.

## Features

The Config Service includes:

*   **Hierarchical Configuration**: Configuration values are organized in a tree structure with paths like `token.tms.<id>` for Token Management Service settings.
*   **Default Values**: Provides sensible defaults for all configuration parameters while allowing overrides.
*   **Validation**: Includes validation mechanisms to ensure configuration values are within acceptable ranges.
*   **Dynamic Updates**: Supports runtime configuration changes for certain parameters.
*   **Integration with FSC**: Built on top of the Fabric Smart Client (FSC) configuration system for consistency with the broader platform.

## Implementation Details

The Config Service is implemented in the `token/services/config` package and provides an interface for accessing configuration values throughout Panurus.

Key aspects of the configuration system:
- Configuration paths follow the pattern: `token.<component>.<setting>`
- Each Token Management Service (TMS) has its own configuration subtree: `token.tms.<tms-id>`
- Version tracking to detect configuration changes
- Enabled/disabled flags for optional components

The service works with the FSC configuration system to provide a consistent configuration experience across the platform.

## Usage

The Config Service is used internally by various SDK components to:
1.  Retrieve Token Management Service configuration (enabled status, driver settings, etc.)
2.  Access driver-specific configuration parameters
3.  Configure internal services (network, storage, identity, etc.)
4.  Manage feature flags and optional components
5.  Version configuration schemas for backward compatibility

Components access the Config Service through dependency injection or by using the provided getter functions for specific configuration domains.