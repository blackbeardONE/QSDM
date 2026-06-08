# QSDM Hive Branding Guide

QSDM Hive ships with default QSDM branding. You can still replace the desktop logo, app name, and theme colors by adding a `branding` folder at the app root.

## Folder Structure

Create this structure next to `src`, `assets`, and `release`:

```text
branding/
  branding.json
  logo.svg
```

## Configuration

`branding.json` should include:

```json
{
  "appName": "QSDM Hive",
  "onboardingTaskID": "Optional task ID",
  "colors": {
    "base": "#0A4D68",
    "depth-100": "#06141B",
    "depth-90": "#0B1F28",
    "depth-80": "#102A35",
    "depth-70": "#173946",
    "depth-60": "#1F4A58",
    "depth-50": "#2E6472",
    "depth-40": "#3F8090",
    "depth-30": "#62A6B4",
    "depth-20": "#9ED7DC",
    "depth-10": "#D8F3F4",
    "highlight": "#F7C948",
    "gradient-start": "rgba(6, 20, 27, 1)",
    "gradient-end": "rgba(10, 77, 104, 0.95)"
  }
}
```

## Requirements

- `logo.svg` should be square, preferably `512x512`.
- The logo should remain readable on dark backgrounds.
- Define every color token shown above.
- `onboardingTaskID` is optional, but must be a valid task ID when provided.

## Validation

Before packaging a branded build:

1. Start the app and verify the launch screen, unlock screen, and header use the new logo.
2. Check that text remains readable on all themed backgrounds.
3. Confirm the onboarding task loads when `onboardingTaskID` is set.

For QSDM Hive support, use https://qsdm.tech.
