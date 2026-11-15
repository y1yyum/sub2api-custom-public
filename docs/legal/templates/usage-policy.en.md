# Acceptable Use Policy (AUP)

**Last Updated:** November 1, 2026  
**Effective Date:** November 1, 2026

This Acceptable Use Policy ("Policy") governs your use of the developer APIs, model endpoints, and web console provided by **ai2e** ("we", "us", or "our"), accessible at **https://web.ai2e.cc**.

By accessing or utilizing any part of the Services, you agree to comply with this Policy in full. Failure to adhere to these rules may result in immediate suspension or termination of your account, revocation of API keys, and forfeiture of account balance without refund.

---

## 1. Scope & Core Principles

This Policy applies to all API calls, prompts, context data, completion requests, and automated workflows submitted through ai2e.

You are strictly responsible for:
1. All traffic, prompts, and outputs generated through your API keys or credentials.
2. Ensuring that your downstream end users, automated agents, or client applications comply with the rules outlined herein.
3. Ensuring that you are not initiating or routing requests from excluded regions, including **Mainland China, Russia, and Iran**.

---

## 2. Upstream AI Provider Policy Alignment

ai2e routes API requests to various third-party artificial intelligence foundational models. **You must strictly comply with the acceptable use policies and service terms of the respective upstream model providers for any model you invoke**, including but not limited to:

- **OpenAI:** [OpenAI Usage Policies](https://openai.com/policies/usage-policies)
- **Anthropic:** [Anthropic Acceptable Use Policy](https://www.anthropic.com/legal/aup)
- **Google DeepMind / Cloud:** [Google Generative AI Prohibited Use Policy](https://policies.google.com/terms/generative-ai/use-policy)
- **DeepSeek:** [DeepSeek Terms of Service and Privacy Policy](https://www.deepseek.com)
- Any other model vendor accessible via your assigned channels or endpoints.

Any action that violates an upstream provider's terms is automatically considered a severe violation of this Policy.

---

## 3. Prohibited Content & Generation Activities

You agree not to use the Services to generate, facilitate, distribute, or promote any of the following:

### 3.1 Severe Harm, Violence & Exploitation
- **Child Sexual Abuse Material (CSAM) or Child Sexual Exploitation and Abuse (CSAE):** Zero tolerance; any detected attempt will result in immediate termination, key blacklisting, and reporting to legal authorities.
- **Suicide & Self-Harm:** Encouraging, instructing, or depicting suicide, cutting, eating disorders, or self-harm.
- **Terrorism & Violent Extremism:** Inciting, planning, promoting, or glorifying terrorist acts, mass casualties, or violent extremist ideologies.
- **Weapons & Explosives:** Instructions, schematics, or optimization algorithms for chemical, biological, radiological, or nuclear weapons (CBRN), explosives, or hazardous precursors.

### 3.2 Hate, Harassment & Defamation
- Generating targeted harassment, intimidation, stalking, or threats of physical harm against individuals.
- Producing hate speech that promotes discrimination, disparagement, or hatred against protected groups based on race, ethnicity, religion, disability, age, nationality, sexual orientation, or gender identity.
- Defamatory, libelous, or maliciously false statements intended to harm reputations.

### 3.3 Deception, Fraud & Disinformation
- Generating fraudulent schemes, phishing templates, financial scams, identity theft workflows, or counterfeit materials.
- Large-scale political astroturfing, election interference, undisclosed automated campaign content, or deepfake/disinformation campaigns.
- Misrepresenting AI-generated output as being authored by a specific real human without permission, or impersonating individuals or public entities.

### 3.4 Non-Consensual Sexual Content
- Generating non-consensual sexual content, sexually explicit deepfakes, or pornography depicting real persons without their verifiable consent.

### 3.5 High-Risk Autonomous Decision-Making
- Utilizing the API to make automated, unreviewed determinations in high-stakes fields:
  - Medical diagnoses or individualized medical treatment advice without licensed physician review.
  - Legal advice or determinations of criminal culpability without qualified legal counsel.
  - Creditworthiness, housing eligibility, or employment determinations subject to anti-discrimination laws.

---

## 4. Prohibited Technical, Territorial & Platform Activities

You agree not to engage in any activities that degrade or undermine the security, integrity, or compliance of ai2e:

1. **Territorial Violations & Geo-Circumvention:**
   - Accessing or attempting to access the Services from prohibited or sanctioned jurisdictions, specifically including **Mainland China, Russia, and Iran**.
   - Utilizing residential proxies, bulletproof VPNs, or network obfuscation tools to bypass ai2e's geographic access restrictions.
2. **Circumvention & Rate Abuse:**
   - Attempting to bypass concurrency limits, token-per-minute (TPM) caps, requests-per-minute (RPM) limits, or IP allowlists.
   - Employing distributed botnets, aggressive multi-key rotating pools, or denial-of-service (DDoS) techniques against the platform.
3. **Reverse Engineering & Extraction:**
   - Attempting to reverse engineer, decompile, or extract underlying model weights, system prompts, safety classifiers, or internal channel credentials.
   - Performing prompt injection or jailbreak attacks aimed at extracting private API credentials, infrastructure details, or other users' data.
4. **Account & Credential Misuse:**
   - Sharing accounts, credentials, or API keys with unauthorized third parties or selling access on unauthorized marketplaces.
   - Credential stuffing, brute-forcing authorization tokens, or unauthorized scanning of platform endpoints.
5. **Unauthorized Public Resale:**
   - Re-routing or commercial reselling of ai2e endpoints as an unvetted public proxy without prior written authorization.

---

## 5. Monitoring, Auditing & Enforcement

### 5.1 Automated Screening & Auditing
To maintain platform stability, prevent malicious activity, and protect upstream channel reputation:
- ai2e employs automated rate-limiting, error spike detection, and token usage anomaly screening.
- Metadata (such as IP addresses, timestamps, model identifiers, error codes, and token counts) is logged and audited.

### 5.2 Enforcement Actions
If we determine, at our sole discretion, that an account or key has violated this Policy, we reserve the right to take immediate action, including:
1. **Throttling:** Imposing strict rate limits or temporary cooldown periods.
2. **Key Revocation:** Invalidating compromised or abusive API keys immediately.
3. **Channel Isolation:** Restricting access to specific models, providers, or fallback pools.
4. **Account Termination:** Permanently suspending the account and invalidating all associated tokens.
5. **Forfeiture:** Retaining unconsumed account balances as liquidated damages for costs incurred by intentional platform abuse.
6. **Reporting:** Referring egregious or illegal conduct (including CSAM and terror threats) to appropriate law enforcement authorities.

---

## 6. Reporting Violations & Support

If you become aware of any security vulnerability, abuse, or violation of this Policy occurring via ai2e, please submit a report immediately through our official customer support channel:

- **Platform Name:** ai2e
- **Official Portal:** https://web.ai2e.cc
- **Official Reporting Channel:** In-Platform Support Ticket System (select category: "Abuse & Security Report")
