# Service-Specific Terms & API Guidelines

**Last Updated:** November 1, 2026  
**Effective Date:** November 1, 2026

These Service-Specific Terms and API Guidelines ("Service Terms") supplement the [Terms of Service](/legal/terms) and govern your technical integration with the developer APIs, model endpoints, and routing systems of **ai2e** ("we", "us", or "our"), accessible at **https://web.ai2e.cc**.

In the event of any conflict between these Service Terms and the general Terms of Service, these Service Terms shall take precedence regarding technical API integration and model-specific usage.

---

## 1. API Keys & Client Security

### 1.1 Secret Credential Confidentiality
- Your API keys grant programmatic access to your account balances and privileges. You must treat them as strictly confidential authentication credentials.
- **Client-Side Exposure Prohibited:** You must not embed API keys directly in client-side applications, mobile app binaries, frontend JavaScript, or public code repositories (e.g., GitHub, GitLab).
- All API requests to ai2e should be initiated from secure, server-side backends where keys are injected via environment variables or secret management vaults.

### 1.2 Key Rotation & Revocation
- You are encouraged to rotate your API keys periodically.
- If you suspect that an API key has been compromised or leaked, you must immediately revoke or delete the key through the ai2e user console and generate a replacement key.

### 1.3 Territorial Origin of API Traffic
All API requests must originate from authorized jurisdictions. As set forth in our [Supported Countries and Regions Policy](/legal/supported-regions), **API requests originating from Mainland China, Russia, Iran, or other excluded territories will be dropped or blocked by network firewalls.**

---

## 2. Rate Limiting, Concurrency & Traffic Rules

### 2.1 Quotas and Limits
To ensure fair resource distribution and protect upstream infrastructure, ai2e enforces multiple layers of traffic control:
- **Requests Per Minute (RPM):** Maximum number of HTTP requests allowed per 60-second window.
- **Tokens Per Minute (TPM):** Maximum volume of cumulative input and output tokens permitted per minute.
- **Concurrent Connections:** Maximum number of simultaneous active or streaming requests permitted per account or API key.
- **Account Credit Limits:** Requests will be blocked immediately if account balance falls below the required threshold or subscription quota expires.

### 2.2 Handling HTTP 429 (Too Many Requests)
When a rate limit is exceeded, ai2e returns an `HTTP 429 (Too Many Requests)` status code:
- Your client application **must implement exponential backoff with jitter** before retrying.
- Rapid, un-backed retry loops following 429 responses are considered abusive traffic and may trigger temporary or permanent IP throttling.

### 2.3 Payload and Streaming Standards
- **Payload Limits:** Requests exceeding maximum payload sizes (e.g., extremely large context windows or high-resolution images beyond channel limits) will be rejected with `HTTP 413 (Payload Too Large)` or `HTTP 400 (Bad Request)`.
- **Server-Sent Events (SSE):** For streaming endpoints (`stream: true`), clients must maintain healthy TCP connections and process chunks promptly. Inactive connections held open without consuming data may be severed by gateway timeouts.

---

## 3. Multi-Channel Routing, Health Checks & Fallbacks

### 3.1 Load Balancing & Channel Scheduling
ai2e utilizes intelligent multi-channel routing algorithms to distribute requests across verified upstream accounts, endpoints, and regional clusters. Routing decisions are made based on real-time channel latency, quota availability, and channel health status.

### 3.2 Automatic Failure Fallback
- In the event that a primary upstream provider channel fails, returns server errors (`5xx`), or encounters temporary rate limits (`429`), the gateway may automatically attempt to fallback to alternative verified channels within the same model group.
- While fallback maximizes availability and minimizes downtime, you acknowledge that response latency and upstream server fingerprints may vary between channels.

### 3.3 Channel Status & Latency Metrics
Any real-time channel latency metrics, uptime percentages, or health indicators displayed in the user portal are informational estimates and do not constitute a formal guarantee of continuous availability.

---

## 4. Privacy Mode & Data Protection Features

### 4.1 Channel-Level Privacy Configurations
Where supported and enabled by platform administrators, certain channel groups or model endpoints may be designated with **Privacy Mode** (e.g., enterprise endpoints, zero-data-retention agreements, or OAuth channels flagged with training set exclusion such as `training_set_cf_blocked`).

### 4.2 Boundaries of Privacy Safeguards
- When routing through privacy-enabled channels, ai2e ensures that requests carry the required upstream opt-out headers and parameters to prevent your inputs from being used for foundation model training.
- Upstream handling of data remains subject to the respective model vendor's published enterprise commitments and security certifications.

---

## 5. Model Deprecation & Lifecycle Management

### 5.1 Upstream Model Sunsets
Third-party model vendors periodically deprecate, update, or retire specific model checkpoints and API versions. 

### 5.2 Deprecation Notice & Aliases
- Whenever possible, ai2e will provide advance notice in the web console regarding planned model retirements.
- We may maintain model aliases (e.g., routing `gpt-4o` to the current stable upstream version). When an underlying version is retired, the alias will point to the recommended successor checkpoint.

---

## 6. Model Output Characteristics & Probabilistic Nature

### 6.1 Non-Deterministic Outputs
Large language models are inherently stochastic and probabilistic. Unless parameters such as `temperature` and `seed` are strictly locked, identical inputs may yield differing outputs across subsequent requests.

### 6.2 Hallucinations & Critical Application Review
- Generated content may include inaccurate statements, outdated information, or fictional claims presented with apparent authority ("hallucinations").
- **Human Review Requirement:** You must incorporate appropriate human verification, sanity checks, and safety filtering into any software utilizing the API for critical workflows (e.g., legal, financial, medical, engineering, or security applications).

---

## 7. Technical Support & Integration Inquiries

All technical support, integration questions, and rate limit tier requests are managed through our official platform:

- **Platform Name:** ai2e
- **Official Service Portal:** https://web.ai2e.cc
- **Official Support Channel:** In-Platform Support Ticket System (accessible via the user console at https://web.ai2e.cc)
