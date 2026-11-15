# Legal & Compliance Documents

This directory contains legal agreements, operational compliance commitments, and user-facing policy templates for **ai2e** (`https://web.ai2e.cc`).

---

## Directory Structure

```
docs/legal/
├── README.md                      # This directory guide
├── admin-compliance.zh.md         # Administrator compliance commitment (Chinese)
├── admin-compliance.en.md         # Administrator compliance commitment (English)
└── templates/                     # User-facing legal policy documents
    ├── terms.en.md                # Terms of Service (ToS)
    ├── usage-policy.en.md         # Acceptable Use Policy (AUP)
    ├── privacy.en.md              # Privacy Policy
    ├── supported-regions.en.md    # Supported Countries and Regions Policy
    └── service-specific-terms.en.md # Service-Specific Terms & API Guidelines
```

---

## 1. Instance Configuration Constants

The documents in `templates/` are configured with the following parameters:
- **Site Name:** `ai2e`
- **Domain / Service Portal:** `https://web.ai2e.cc`
- **Customer Support Channel:** In-Platform Support Ticket System (within user console)
- **Effective / Updated Date:** `November 1, 2026` (`2026-11-01`)
- **Governing Law / Jurisdiction:** Republic of Zambia (exclusive jurisdiction of courts in Zambia)
- **Territorial Exclusions:** Explicitly prohibits providing services or access to users in **Mainland China, Russia, Iran**, Cuba, North Korea, Syria, Crimea/Donetsk/Luhansk, and other sanctioned territories.

---

## 2. Document-to-Route Mapping

| File | Default Route Slug | Purpose |
| :--- | :--- | :--- |
| `terms.en.md` | `/legal/terms` | General Terms of Service, billing, SLA disclaimers, Zambia jurisdiction, territorial exclusions |
| `usage-policy.en.md` | `/legal/usage-policy` | Acceptable Use Policy & upstream AI alignment, anti-circumvention |
| `privacy.en.md` | `/legal/privacy` | Technical data handling, prompt proxying & retention rules, support ticket privacy requests |
| `supported-regions.en.md` | `/legal/supported-regions` | Trade sanctions, explicit exclusion of Mainland China, Russia, Iran |
| `service-specific-terms.en.md` | `/legal/service-specific-terms` | API keys, rate limits, 429 backoff, multi-channel fallback, privacy mode |
