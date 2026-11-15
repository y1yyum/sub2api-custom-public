<script>
import { h, ref, computed, onMounted, onUnmounted } from "vue";
import i18n from "@/i18n";
const t = (...args) => i18n.global.t(...args);
import { apiClient } from "@/api/client";
import AppLayout from "@/components/layout/AppLayout.vue";
import TotpStepUpDialog from "@/components/auth/TotpStepUpDialog.vue";
import { useStepUp, isStepUpCancelled } from "@/composables/useStepUp";
const SiteButton = (props, { slots, attrs }) =>
  h(
    "button",
    {
      ...attrs,
      type: "button",
      class: [
        "btn",
        props.variant === "primary" ? "btn-primary" : "btn-secondary",
      ],
      disabled: props.disabled,
    },
    slots.default?.(),
  );
SiteButton.props = ["variant", "disabled"];
const SiteCard = (_, { slots }) =>
  h("section", { class: "card p-5" }, slots.default?.());
const SiteBadge = (_, { slots }) =>
  h("span", { class: "badge badge-gray" }, slots.default?.());
const statuses = () => ({
  healthy: t("admin.upstreamSites.healthy"),
  authentication: t("admin.upstreamSites.credentialsOrAccessDenied"),
  unknown: t("admin.upstreamSites.accountBalanceUnavailable"),
  unreachable: t("admin.upstreamSites.upstreamUnreachable"),
  rate_limited: t("admin.upstreamSites.queryRateLimited"),
  monitor_failure: t("admin.upstreamSites.monitorRequestFailed"),
  probe_unhealthy: t("admin.upstreamSites.probeResponseMismatch"),
  configuration: t("admin.upstreamSites.configurationError"),
});
const protocols = [
  "openai_chat_completions",
  "openai_responses",
  "anthropic_messages",
  "gemini_generate_content",
  "custom",
];
const adapters = {
  sub2api: "Sub2API",
  new_api: "New API",
  deepseek_balance: "DeepSeek",
  kimi_balance: "Kimi",
  zivv: "Zivv",
  derouter: "DeRouter",
};
function normalizeURL(value) {
  try {
    const u = new URL(value);
    return u.origin.toLowerCase() + u.pathname.replace(/\/+$/, "");
  } catch {
    return "";
  }
}
function displayTime(value) {
  return value
    ? new Date(value).toLocaleString()
    : t("admin.upstreamSites.notQueriedYet");
}
export default {
  setup() {
    const stepUp = useStepUp();
    const sites = ref([]),
      loading = ref(false),
      message = ref(""),
      error = ref(""),
      search = ref("");
    const control = ref({
      available: false,
      status: { holds: [], events: [] },
    });
    const tab = ref("sites"),
      selected = ref(null),
      form = ref(null),
      secrets = ref({}),
      providers = ref([]),
      bindings = ref({}),
      bindingVersion = ref(0),
      bindingDirty = ref(false);
    const history = ref([]),
      historyTotal = ref(0),
      offset = ref(0),
      daily = ref([]),
      alerts = ref([]);
    const filtered = computed(() =>
      sites.value.filter((s) =>
        (s.config.name + " " + s.config.base_url)
          .toLowerCase()
          .includes(search.value.toLowerCase()),
      ),
    );
    async function api(method, path, body, query) {
      const request = () =>
        apiClient.request({
          method,
          url: "/admin/upstream-sites" + path,
          data: body,
          params: query,
        });
      const r = await (method === "GET" ? request() : stepUp.run(request));
      return r.data;
    }
    async function perform(work) {
      if (loading.value) return;
      loading.value = true;
      error.value = "";
      message.value = "";
      try {
        await work();
      } catch (e) {
        if (!isStepUpCancelled(e))
          error.value = e.message || t("admin.upstreamSites.operationFailed");
      } finally {
        loading.value = false;
      }
    }
    async function refresh() {
      const r = await api("GET", "/sites");
      sites.value = r.sites;
      control.value = await api("GET", "/control");
      if (selected.value)
        selected.value =
          sites.value.find((s) => s.id === selected.value.id) || null;
    }
    function button(label, action, secondary = true) {
      return h(
        SiteButton,
        {
          variant: secondary ? "secondary" : "primary",
          disabled: loading.value,
          onClick: () => perform(action),
        },
        () => label,
      );
    }
    function input(value, change, attrs = {}) {
      return h("input", {
        ...attrs,
        value,
        onInput: (e) => change(e.target.value),
      });
    }
    function field(label, value, change, attrs = {}) {
      return h("label", { class: "us-field" }, [
        h("span", label),
        input(value, change, attrs),
      ]);
    }
    function select(value, choices, change) {
      return h(
        "select",
        { value, onChange: (e) => change(e.target.value) },
        choices.map(([v, label]) => h("option", { value: v }, label)),
      );
    }
    function toggle(label, value, change) {
      return h("label", { class: "us-check" }, [
        h("input", {
          type: "checkbox",
          checked: value,
          onChange: (e) => change(e.target.checked),
        }),
        label,
      ]);
    }
    function table(headings, rows) {
      return h("div", { class: "us-scroll" }, [
        h("table", [
          h(
            "thead",
            h(
              "tr",
              headings.map((v) => h("th", v)),
            ),
          ),
          h(
            "tbody",
            rows.length
              ? rows.map((row) =>
                  h(
                    "tr",
                    row.map((v) => h("td", v)),
                  ),
                )
              : h(
                  "tr",
                  h(
                    "td",
                    { colspan: headings.length },
                    t("admin.upstreamSites.noRecords"),
                  ),
                ),
          ),
        ]),
      ]);
    }
    function openEditor(site) {
      form.value = site
        ? JSON.parse(JSON.stringify(site))
        : {
            config_version: 0,
            archived: false,
            config: {
              name: "",
              notes: "",
              base_url: "",
              adapter: "sub2api",
              endpoints: [],
              auto_query: false,
              interval_minutes: 60,
              timezone: "Asia/Shanghai",
              raw_retention_days: 90,
              summary_retention_days: 365,
              policy: {
                mode: "off",
                auto_recover: false,
                thresholds: [],
                balance_match: "any",
                low_count: 1,
                failure_count: 3,
                recovery_count: 2,
                min_suspend_seconds: 120,
              },
              probes: [],
              alerts: {
                daily_metric: "estimated",
                control: false,
                unit: "USD",
                low_balance: "",
                daily_spend: "",
                failures: 0,
                email: "",
              },
            },
          };
      secrets.value = {
        key: "",
        wallet_token: "",
        wallet_user_id: "",
        proxy_url: "",
      };
      tab.value = "edit";
    }
    async function save() {
      const f = form.value;
      if (f.config.policy.mode === "off") f.config.policy.auto_recover = false;
      await api(f.id ? "PUT" : "POST", f.id ? `/sites/${f.id}` : "/sites", {
        config: f.config,
        config_version: f.config_version,
        archived: f.archived,
        clear_key: !!f.clear_key,
        clear_wallet_token: !!f.clear_wallet_token,
        clear_proxy: !!f.clear_proxy,
        secrets: secrets.value,
      });
      secrets.value = {};
      form.value = null;
      tab.value = "sites";
      await refresh();
      message.value = t("admin.upstreamSites.siteSaved");
    }
    async function query(site, source = "balance") {
      const o = await api("POST", `/sites/${site.id}/query`, undefined, {
        source,
      });
      await refresh();
      if (tab.value === "history" && selected.value)
        await loadDetails(selected.value, offset.value);
      message.value = `${site.config.name}：${statuses()[o.outcome] || o.outcome}`;
    }
    async function queryAll() {
      const queue = sites.value.filter((s) => !s.archived).slice();
      let success = 0,
        failed = 0;
      await Promise.all(
        Array.from({ length: 4 }, async () => {
          while (queue.length) {
            const s = queue.shift();
            try {
              const o = await api("POST", `/sites/${s.id}/query`);
              if (o.outcome === "healthy") success++;
              else failed++;
            } catch {
              failed++;
            }
          }
        }),
      );
      await refresh();
      message.value = t("admin.upstreamSites.querySummary", {
        success,
        failed,
      });
    }
    async function loadDetails(site, start = 0) {
      selected.value = site;
      offset.value = start;
      const [hist, days, events] = await Promise.all([
        api("GET", `/sites/${site.id}/history`, undefined, {
          offset: String(start),
        }),
        api("GET", `/sites/${site.id}/daily`),
        api("GET", `/sites/${site.id}/alerts`),
      ]);
      history.value = hist.items;
      historyTotal.value = hist.total;
      daily.value = days;
      alerts.value = events;
      tab.value = "history";
    }
    async function loadBindings() {
      const [p, b] = await Promise.all([
        api("GET", "/providers"),
        api("GET", "/bindings"),
      ]);
      providers.value = p;
      bindings.value = b.bindings;
      bindingVersion.value = b.version;
      bindingDirty.value = false;
      const accountIDs = new Set(p.map((account) => String(account.id)));
      for (const id of Object.keys(bindings.value)) {
        if (!accountIDs.has(id)) {
          delete bindings.value[id];
          bindingDirty.value = true;
        }
      }
      if (bindingDirty.value)
        message.value = t("admin.upstreamSites.staleBindings");
      tab.value = "bindings";
    }
    async function suggestBindings() {
      let matches = 0;
      for (const p of providers.value) {
        if (bindings.value[p.id]) continue;
        const key = normalizeURL(p.base_url);
        if (!key) continue;
        const candidates = sites.value.filter(
          (s) =>
            !s.archived &&
            [s.config.base_url, ...s.config.endpoints.map((e) => e.url)].some(
              (u) => normalizeURL(u) === key,
            ),
        );
        if (candidates.length === 1) {
          bindings.value[p.id] = candidates[0].id;
          matches++;
        }
      }
      bindingDirty.value = bindingDirty.value || matches > 0;
      message.value = t("admin.upstreamSites.bindingSummary", { matches });
    }
    async function saveBindings() {
      const r = await api("PUT", "/bindings", {
        bindings: bindings.value,
        version: bindingVersion.value,
      });
      bindingVersion.value = r.version;
      bindingDirty.value = false;
      await refresh();
      message.value = t(
        "admin.upstreamSites.bindingsSavedAccountForwardingSettingsAreUnchanged",
      );
    }
    function endpointFields(c) {
      return h("fieldset", [
        h("legend", t("admin.upstreamSites.protocolEndpoints")),
        ...c.endpoints.map((e, i) =>
          h("div", { class: "us-row" }, [
            field(t("admin.upstreamSites.name"), e.name, (v) => (e.name = v)),
            field(
              t("admin.upstreamSites.protocol"),
              e.protocol,
              (v) => (e.protocol = v),
              { list: "us-protocols" },
            ),
            field("BaseURL", e.url, (v) => (e.url = v), { type: "url" }),
            toggle(
              t("admin.upstreamSites.defaultEndpointForProtocol"),
              e.default,
              (v) => (e.default = v),
            ),
            button(t("admin.upstreamSites.copyAddress"), () =>
              navigator.clipboard.writeText(e.url),
            ),
            button(t("admin.upstreamSites.remove"), async () =>
              c.endpoints.splice(i, 1),
            ),
          ]),
        ),
        h(
          "datalist",
          { id: "us-protocols" },
          protocols.map((p) => h("option", { value: p })),
        ),
        button(t("admin.upstreamSites.addEndpoint"), async () =>
          c.endpoints.push({
            id: crypto.randomUUID(),
            name: "",
            protocol: "openai_chat_completions",
            url: c.base_url,
            default: false,
          }),
        ),
      ]);
    }
    function probeFields(c) {
      return h("fieldset", [
        h("legend", t("admin.upstreamSites.scheduledProbes")),
        ...c.probes.map((p, i) =>
          h("div", { class: "us-probe" }, [
            h("div", { class: "us-row" }, [
              field(t("admin.upstreamSites.probeName"), p.id, (v) => {
                const key = secrets.value.probe_keys?.[p.id];
                if (secrets.value.probe_keys)
                  delete secrets.value.probe_keys[p.id];
                p.id = v;
                if (key) secrets.value.probe_keys[p.id] = key;
              }),
              field(
                t("admin.upstreamSites.fullUrl"),
                p.url,
                (v) => (p.url = v),
                { type: "url" },
              ),
              field(
                t("admin.upstreamSites.intervalMinutes"),
                p.interval_minutes,
                (v) => (p.interval_minutes = Number(v)),
                { type: "number", min: 1, max: 1440 },
              ),
            ]),
            h("div", { class: "us-row" }, [
              field(
                t("admin.upstreamSites.responseJsonField"),
                p.json_path,
                (v) => (p.json_path = v),
                { placeholder: t("admin.upstreamSites.forExampleSuccess") },
              ),
              field(
                t("admin.upstreamSites.timeoutSeconds"),
                p.timeout_seconds,
                (v) => (p.timeout_seconds = Number(v)),
                { type: "number", min: 3, max: 60 },
              ),
              field(
                t("admin.upstreamSites.successStatusCodesCommaSeparated"),
                p.success_codes.join(","),
                (v) => (p.success_codes = v.split(",").map(Number)),
              ),
              field(
                t("admin.upstreamSites.probeKeyBlankKeepsSavedValue"),
                secrets.value.probe_keys?.[p.id] || "",
                (v) => {
                  secrets.value.probe_keys ||= {};
                  secrets.value.probe_keys[p.id] = v;
                },
                { type: "password", autocomplete: "new-password" },
              ),
              field(
                t("admin.upstreamSites.expectedValue"),
                p.expected,
                (v) => (p.expected = v),
              ),
              toggle(
                t("admin.upstreamSites.useForSiteSuspension"),
                p.required,
                (v) => (p.required = v),
              ),
              toggle(
                t("admin.upstreamSites.useWalletKeySameOriginOnly"),
                p.use_wallet_key,
                (v) => (p.use_wallet_key = v),
              ),
              button(t("admin.upstreamSites.remove"), async () => {
                if (secrets.value.probe_keys)
                  delete secrets.value.probe_keys[p.id];
                c.probes.splice(i, 1);
              }),
            ]),
          ]),
        ),
        button(t("admin.upstreamSites.addProbe"), async () => {
          let number = 1;
          while (c.probes.some((probe) => probe.id === `probe-${number}`))
            number++;
          c.probes.push({
            id: `probe-${number}`,
            url: c.base_url,
            interval_minutes: 5,
            timeout_seconds: 10,
            json_path: "success",
            expected: "true",
            use_wallet_key: false,
            success_codes: [200],
            required: false,
          });
        }),
      ]);
    }
    function controlFields(c) {
      const p = c.policy;
      return h("fieldset", [
        h(
          "legend",
          t("admin.upstreamSites.automaticAccountSuspensionAndRecovery"),
        ),
        !control.value.available
          ? h(
              "p",
              { class: "us-error" },
              t(
                "admin.upstreamSites.controlIsUnavailableBalanceMonitoringRemainsAvailable",
              ),
            )
          : null,
        h("label", { class: "us-field" }, [
          t("admin.upstreamSites.controlMode"),
          select(
            p.mode,
            [
              ["off", t("admin.upstreamSites.off")],
              [
                "balance",
                t("admin.upstreamSites.balanceThresholdBalanceEndpointOutage"),
              ],
              ["probe", t("admin.upstreamSites.scheduledProbes")],
              ["combined", t("admin.upstreamSites.balanceAndProbes")],
            ],
            (v) => {
              p.mode = v;
              if (v === "off") p.auto_recover = false;
            },
          ),
        ]),
        p.mode !== "off"
          ? h("div", [
              toggle(
                t(
                  "admin.upstreamSites.automaticallyRecoverWhenConditionsAreMet",
                ),
                p.auto_recover,
                (v) => (p.auto_recover = v),
              ),
              h("div", { class: "us-grid" }, [
                field(
                  t("admin.upstreamSites.consecutiveLowBalanceObservations"),
                  p.low_count,
                  (v) => (p.low_count = Number(v)),
                  { type: "number", min: 1, max: 100 },
                ),
                field(
                  t("admin.upstreamSites.consecutiveOutagesProbeFailures"),
                  p.failure_count,
                  (v) => (p.failure_count = Number(v)),
                  { type: "number", min: 1, max: 100 },
                ),
                field(
                  t("admin.upstreamSites.consecutiveHealthyObservations"),
                  p.recovery_count,
                  (v) => (p.recovery_count = Number(v)),
                  { type: "number", min: 1, max: 100 },
                ),
                field(
                  t("admin.upstreamSites.minimumSuspensionDurationSeconds"),
                  p.min_suspend_seconds,
                  (v) => (p.min_suspend_seconds = Number(v)),
                  { type: "number", min: 0, max: 86400 },
                ),
              ]),
              ["balance", "combined"].includes(p.mode)
                ? h("div", [
                    h("label", { class: "us-field" }, [
                      t("admin.upstreamSites.multipleCurrencies"),
                      select(
                        p.balance_match,
                        [
                          [
                            "any",
                            t(
                              "admin.upstreamSites.suspendIfAnyIsLowRecoverWhen",
                            ),
                          ],
                          [
                            "all",
                            t(
                              "admin.upstreamSites.suspendIfAllAreLowRecoverWhen",
                            ),
                          ],
                        ],
                        (v) => (p.balance_match = v),
                      ),
                    ]),
                    ...p.thresholds.map((threshold, i) =>
                      h("div", { class: "us-row" }, [
                        select(
                          threshold.unit,
                          [
                            ["USD", "USD"],
                            ["CNY", "CNY"],
                            ["TOKENS", "TOKENS"],
                          ],
                          (v) => (threshold.unit = v),
                        ),
                        field(
                          t("admin.upstreamSites.suspendBelowThisBalance"),
                          threshold.low,
                          (v) => (threshold.low = v),
                        ),
                        field(
                          t("admin.upstreamSites.recoverAtThisBalance"),
                          threshold.recover,
                          (v) => (threshold.recover = v),
                        ),
                        button(
                          t("admin.upstreamSites.removeThreshold"),
                          async () => p.thresholds.splice(i, 1),
                        ),
                      ]),
                    ),
                    button(
                      t("admin.upstreamSites.addBalanceThreshold"),
                      async () =>
                        p.thresholds.push({
                          unit: "USD",
                          low: "1",
                          recover: "5",
                        }),
                    ),
                  ])
                : null,
              ["probe", "combined"].includes(p.mode)
                ? h(
                    "p",
                    t("admin.upstreamSites.selectAtLeastOneRequiredProbeAll"),
                  )
                : null,
            ])
          : null,
      ]);
    }
    function editor() {
      const f = form.value,
        c = f.config;
      return h(SiteCard, null, () => [
        h(
          "h2",
          f.id
            ? t("admin.upstreamSites.editUpstreamSite")
            : t("admin.upstreamSites.createUpstreamSite"),
        ),
        h("div", { class: "us-grid" }, [
          field(t("admin.upstreamSites.name"), c.name, (v) => (c.name = v)),
          field(
            t("admin.upstreamSites.balanceQueryBaseurl"),
            c.base_url,
            (v) => (c.base_url = v),
            { type: "url" },
          ),
          h("label", { class: "us-field" }, [
            t("admin.upstreamSites.upstreamAdapter"),
            select(c.adapter, Object.entries(adapters), (v) => (c.adapter = v)),
          ]),
          field(
            t("admin.upstreamSites.balanceQueryKey"),
            secrets.value.key,
            (v) => (secrets.value.key = v),
            {
              type: "password",
              autocomplete: "new-password",
              placeholder: f.has_key
                ? t("admin.upstreamSites.savedLeaveBlankToKeep")
                : t("admin.upstreamSites.enterAKeyThatCanReadThe"),
            },
          ),
          field(
            t("admin.upstreamSites.queryIntervalMinutes"),
            c.interval_minutes,
            (v) => (c.interval_minutes = Number(v)),
            { type: "number", min: 5, max: 1440 },
          ),
          field(
            t("admin.upstreamSites.statisticsTimezone"),
            c.timezone,
            (v) => (c.timezone = v),
          ),
        ]),
        c.adapter === "new_api"
          ? h("div", { class: "us-row" }, [
              field(
                t("admin.upstreamSites.accountAccessTokenOptional"),
                secrets.value.wallet_token,
                (v) => (secrets.value.wallet_token = v),
                { type: "password", autocomplete: "new-password" },
              ),
              field(
                t("admin.upstreamSites.accountUserIdOptional"),
                secrets.value.wallet_user_id,
                (v) => (secrets.value.wallet_user_id = v),
              ),
            ])
          : null,
        toggle(
          t("admin.upstreamSites.automaticallyQueryBalance"),
          c.auto_query,
          (v) => (c.auto_query = v),
        ),
        endpointFields(c),
        probeFields(c),
        controlFields(c),
        h("fieldset", [
          h("legend", t("admin.upstreamSites.alertThresholds")),
          h("div", { class: "us-grid" }, [
            h("label", { class: "us-field" }, [
              t("admin.upstreamSites.currency"),
              select(
                c.alerts.unit || "USD",
                [
                  ["USD", "USD"],
                  ["CNY", "CNY"],
                  ["TOKENS", "TOKENS"],
                ],
                (v) => (c.alerts.unit = v),
              ),
            ]),
            field(
              t("admin.upstreamSites.lowBalanceBlankDisables"),
              c.alerts.low_balance,
              (v) => (c.alerts.low_balance = v),
            ),
            field(
              t("admin.upstreamSites.dailySpendBlankDisables"),
              c.alerts.daily_spend,
              (v) => (c.alerts.daily_spend = v),
            ),
            field(
              t("admin.upstreamSites.consecutiveFailures0Disables"),
              c.alerts.failures,
              (v) => (c.alerts.failures = Number(v)),
              { type: "number", min: 0, max: 100 },
            ),
            h("label", { class: "us-field" }, [
              t("admin.upstreamSites.consumptionMetric"),
              select(
                c.alerts.daily_metric,
                [
                  [
                    "estimated",
                    t("admin.upstreamSites.estimatedBalanceDecrease"),
                  ],
                  [
                    "account",
                    t("admin.upstreamSites.accountCumulativeConsumptionDelta"),
                  ],
                ],
                (v) => (c.alerts.daily_metric = v),
              ),
            ]),
          ]),
          field(
            t("admin.upstreamSites.alertEmailBlankDisablesEmail"),
            c.alerts.email,
            (v) => (c.alerts.email = v),
            { type: "email" },
          ),
          toggle(
            t("admin.upstreamSites.emailSuspensionAndRecoveryEvents"),
            c.alerts.control,
            (v) => (c.alerts.control = v),
          ),
        ]),
        field(
          t("admin.upstreamSites.rawRecordRetentionDays"),
          c.raw_retention_days,
          (v) => (c.raw_retention_days = Number(v)),
          { type: "number", min: 7, max: 3650 },
        ),
        field(
          t("admin.upstreamSites.summaryAndEventRetentionDays"),
          c.summary_retention_days,
          (v) => (c.summary_retention_days = Number(v)),
          { type: "number", min: c.raw_retention_days, max: 3650 },
        ),
        toggle(
          t("admin.upstreamSites.clearBalanceKey"),
          f.clear_key,
          (v) => (f.clear_key = v),
        ),
        toggle(
          t("admin.upstreamSites.clearAccountTokenAndUserId"),
          f.clear_wallet_token,
          (v) => (f.clear_wallet_token = v),
        ),
        toggle(
          t("admin.upstreamSites.clearMonitoringProxy"),
          f.clear_proxy,
          (v) => (f.clear_proxy = v),
        ),
        field(
          t("admin.upstreamSites.monitoringProxyBlankKeepsSavedValue"),
          secrets.value.proxy_url,
          (v) => (secrets.value.proxy_url = v),
          {
            type: "password",
            autocomplete: "new-password",
            placeholder: f.has_proxy
              ? t("admin.upstreamSites.proxyConfigured")
              : t("admin.upstreamSites.httpOrHttps"),
          },
        ),
        h("label", { class: "us-field" }, [
          t("admin.upstreamSites.notes"),
          h("textarea", {
            value: c.notes,
            rows: 3,
            onInput: (e) => (c.notes = e.target.value),
          }),
        ]),
        toggle(
          t("admin.upstreamSites.archiveUnbindAccountsFirstStopsMonitoring"),
          f.archived,
          (v) => (f.archived = v),
        ),
        h("div", { class: "us-actions" }, [
          button(t("admin.upstreamSites.save"), save, false),
          button(t("admin.upstreamSites.cancel"), async () => {
            secrets.value = {};
            form.value = null;
            tab.value = "sites";
          }),
        ]),
      ]);
    }
    function siteControl(s) {
      const reasons = Object.keys(s.state.reasons || {});
      const automatic = reasons.length
        ? "blocked"
        : s.config.policy.mode === "off"
          ? "off"
          : "clear";
      const label = t(`admin.upstreamSites.autoStatus_${automatic}`);
      const reasonLabels = reasons.map((reason) =>
        reason === "balance_low"
          ? t("admin.upstreamSites.reasonLowBalance")
          : reason === "balance_endpoint_unreachable"
            ? t("admin.upstreamSites.reasonBalanceOutage")
            : t("admin.upstreamSites.reasonProbe", {
                name: reason.replace("probe_unreachable:", ""),
              }),
      );
      return h("div", { class: "us-control" }, [
        h(
          "button",
          {
            class: ["btn", s.state.manual ? "btn-secondary" : "btn-primary"],
            disabled: loading.value || s.archived,
            "aria-pressed": !s.state.manual,
            title: t(
              s.state.manual
                ? "admin.upstreamSites.clickToEnable"
                : "admin.upstreamSites.clickToDisable",
            ),
            onClick: () =>
              perform(async () => {
                await api("PUT", `/sites/${s.id}/manual`, {
                  enabled: !!s.state.manual,
                  state_version: s.state.version,
                });
                await refresh();
              }),
          },
          t(
            s.state.manual
              ? "admin.upstreamSites.manuallyDisabled"
              : "admin.upstreamSites.manuallyEnabled",
          ),
        ),
        h(
          "div",
          {
            class: "us-auto-state",
            role: "status",
            title: reasonLabels.join("、") || label,
            "aria-label": label,
          },
          [
            h("span", {
              class: ["us-state-dot", `us-state-${automatic}`],
              "aria-hidden": "true",
            }),
            label,
          ],
        ),
        h(
          "small",
          t(
            s.archived
              ? "admin.upstreamSites.archived"
              : s.state.manual || reasons.length
                ? "admin.upstreamSites.siteGateBlocked"
                : "admin.upstreamSites.siteGateOpen",
          ),
        ),
      ]);
    }
    function costCell(s) {
      const format = (value) => value ?? t("admin.upstreamSites.unknown");
      return h("div", [
        ...(s.costs || []).map((cost) =>
          h("div", [
            h("strong", `${format(cost.amount)} ${cost.unit}`),
            h(
              "div",
              t("admin.upstreamSites.todayCost", {
                amount: format(
                  s.today?.find((day) => day.unit === cost.unit)?.upstream_cost,
                ),
                unit: cost.unit,
              }),
            ),
            cost.gap
              ? h("small", t("admin.upstreamSites.missingObservations"))
              : null,
          ]),
        ),
        !(s.costs || []).length ? t("admin.upstreamSites.unknown") : null,
      ]);
    }
    function overview() {
      return h(SiteCard, null, () => [
        h("div", { class: "us-actions" }, [
          input(search.value, (v) => (search.value = v), {
            placeholder: t("admin.upstreamSites.searchSiteNameOrAddress"),
            "aria-label": t("admin.upstreamSites.searchSites"),
          }),
          button(t("admin.upstreamSites.refreshList"), refresh),
          button(t("admin.upstreamSites.queryAllBalances"), queryAll),
          button(
            t("admin.upstreamSites.addSite"),
            async () => openEditor(null),
            false,
          ),
        ]),
        table(
          [
            t("admin.upstreamSites.siteBalanceQueryAddress"),
            t("admin.upstreamSites.accounts"),
            t("admin.upstreamSites.latestSuccessfulBalance"),
            t("admin.upstreamSites.retainedCost"),
            t("admin.upstreamSites.latestObservation"),
            t("admin.upstreamSites.automaticQueryControl"),
            t("admin.upstreamSites.siteControl"),
            t("admin.upstreamSites.actions"),
          ],
          filtered.value.map((s) => [
            h("div", [
              h("strong", s.config.name),
              h("div", { class: "us-url" }, s.config.base_url),
              s.archived ? h("small", t("admin.upstreamSites.archived")) : null,
            ]),
            String(s.provider_count),
            s.latest
              ? h("div", [
                  s.latest.balances.map((b) =>
                    h("div", `${b.available} ${b.unit}`),
                  ),
                  h("small", displayTime(s.latest.at)),
                ])
              : t("admin.upstreamSites.unknown"),
            costCell(s),
            h("div", [
              h(
                SiteBadge,
                {
                  tone:
                    s.last_observation?.outcome === "healthy"
                      ? "success"
                      : "warning",
                },
                () =>
                  statuses()[s.last_observation?.outcome] ||
                  t("admin.upstreamSites.notQueriedYet"),
              ),
              h("div", displayTime(s.last_observation?.at)),
            ]),
            h("div", [
              s.config.auto_query && !s.archived
                ? t("admin.upstreamSites.everyMinutes", {
                    minutes: s.config.interval_minutes,
                  })
                : t("admin.upstreamSites.queryDisabled"),
              h(
                "small",
                s.config.policy.auto_recover
                  ? t("admin.upstreamSites.automaticRecoveryEnabled")
                  : t("admin.upstreamSites.automaticRecoveryDisabled"),
              ),
            ]),
            siteControl(s),
            h("div", { class: "us-actions" }, [
              button(t("admin.upstreamSites.query"), () => query(s)),
              button(t("admin.upstreamSites.edit"), async () => openEditor(s)),
              button(t("admin.upstreamSites.records"), () => loadDetails(s)),
              button(t("admin.upstreamSites.delete"), async () => {
                if (
                  !window.confirm(
                    t("admin.upstreamSites.deleteConfirm", {
                      name: s.config.name,
                    }),
                  )
                )
                  return;
                await api("DELETE", `/sites/${s.id}`, undefined, {
                  config_version: s.config_version,
                });
                await refresh();
              }),
            ]),
          ]),
        ),
      ]);
    }
    function providerStatus(p) {
      const hold = (control.value.status?.holds || []).find(
        (x) => x.provider_id === p.id,
      );
      return hold
        ? t("admin.upstreamSites.restrictedAccountStatus", {
            status: p.status,
            selection: hold.manual_schedulable
              ? t("admin.upstreamSites.enabled")
              : t("admin.upstreamSites.disabled"),
          })
        : `${p.status} / ${p.schedulable ? t("admin.upstreamSites.schedulable") : t("admin.upstreamSites.notSchedulable")}`;
    }
    function bindingPage() {
      return h(SiteCard, null, () => [
        h("h2", t("admin.upstreamSites.accountBindings")),
        h("div", { class: "us-actions" }, [
          button(
            t("admin.upstreamSites.scanAndSuggestBindings"),
            suggestBindings,
          ),
          button(t("admin.upstreamSites.saveBindings"), saveBindings, false),
          bindingDirty.value
            ? h("strong", t("admin.upstreamSites.unsavedChanges"))
            : null,
        ]),
        table(
          [
            t("admin.upstreamSites.accounts"),
            t("admin.upstreamSites.currentBaseurl"),
            t("admin.upstreamSites.manualStatus"),
            t("admin.upstreamSites.upstreamSites"),
          ],
          providers.value.map((p) => [
            p.name,
            h("span", { class: "us-url" }, p.base_url),
            providerStatus(p),
            select(
              String(bindings.value[p.id] || ""),
              [
                ["", t("admin.upstreamSites.unboundOfficial")],
                ...sites.value
                  .filter((s) => !s.archived)
                  .map((s) => [String(s.id), s.config.name]),
              ],
              (v) => {
                if (v) bindings.value[p.id] = Number(v);
                else delete bindings.value[p.id];
                bindingDirty.value = true;
              },
            ),
          ]),
        ),
      ]);
    }
    function details() {
      const s = selected.value;
      return h(SiteCard, null, () => [
        h("h2", t("admin.upstreamSites.siteRecords", { name: s.config.name })),
        h("div", { class: "us-actions" }, [
          button(t("admin.upstreamSites.refreshRecords"), () =>
            loadDetails(s, offset.value),
          ),
          ...s.config.probes.map((p) =>
            button(t("admin.upstreamSites.runProbe", { name: p.id }), () =>
              query(s, p.id),
            ),
          ),
        ]),
        h("h3", t("admin.upstreamSites.dailyConsumption")),
        table(
          [
            t("admin.upstreamSites.date"),
            t("admin.upstreamSites.currency"),
            t("admin.upstreamSites.upstreamCost"),
            t("admin.upstreamSites.estimatedBalanceDecrease"),
            t("admin.upstreamSites.accountConsumption"),
            t("admin.upstreamSites.coveredSeconds"),
            t("admin.upstreamSites.quality"),
          ],
          daily.value.map((d) => [
            d.date,
            d.unit,
            d.upstream_cost ?? t("admin.upstreamSites.unknown"),
            d.estimated_spend ?? t("admin.upstreamSites.unknown"),
            d.account_spend ?? t("admin.upstreamSites.unknown"),
            String(d.covered_seconds),
            [
              d.gap ? t("admin.upstreamSites.missingObservations") : "",
              d.cross_day ? t("admin.upstreamSites.allocatedAcrossDays") : "",
              d.available_only
                ? t("admin.upstreamSites.availableBalanceOnly")
                : "",
            ]
              .filter(Boolean)
              .join("；"),
          ]),
        ),
        h("h3", t("admin.upstreamSites.queryAndProbeHistory")),
        table(
          [
            t("admin.upstreamSites.time"),
            t("admin.upstreamSites.source"),
            t("admin.upstreamSites.outcome"),
            t("admin.upstreamSites.accountBalance"),
            t("admin.upstreamSites.diagnostic"),
          ],
          history.value.map((o) => [
            displayTime(o.at),
            o.source,
            statuses()[o.outcome] || o.outcome,
            (o.balances || [])
              .map((b) => `${b.available} ${b.unit}`)
              .join(" / ") || t("admin.upstreamSites.unknown"),
            `${o.code}${o.http_status ? ` (HTTP ${o.http_status})` : ""}`,
          ]),
        ),
        h("div", { class: "us-actions" }, [
          button(t("admin.upstreamSites.previousPage"), () =>
            loadDetails(s, Math.max(0, offset.value - 100)),
          ),
          h(
            "span",
            t("admin.upstreamSites.pagination", {
              total: historyTotal.value,
              page: Math.floor(offset.value / 100) + 1,
            }),
          ),
          button(t("admin.upstreamSites.nextPage"), () =>
            loadDetails(
              s,
              offset.value + 100 < historyTotal.value
                ? offset.value + 100
                : offset.value,
            ),
          ),
        ]),
        h("h3", t("admin.upstreamSites.controlHistory")),
        table(
          [
            t("admin.upstreamSites.time"),
            t("admin.upstreamSites.accountId"),
            t("admin.upstreamSites.actions"),
            t("admin.upstreamSites.manualOrSystemSelection"),
          ],
          alerts.value
            .filter((e) =>
              [
                "suspend",
                "release",
                "manual_enable",
                "manual_disable",
              ].includes(e.kind),
            )
            .map((e) => [
              displayTime(e.at),
              e.provider_id == null ? "—" : String(e.provider_id),
              {
                suspend: t("admin.upstreamSites.siteSuspendedLabel"),
                release: t("admin.upstreamSites.siteRestrictionReleased"),
                manual_enable: t("admin.upstreamSites.manuallyEnabled"),
                manual_disable: t("admin.upstreamSites.manuallyDisabled"),
                external_switch: t(
                  "admin.upstreamSites.manualOrSystemSwitchChanged",
                ),
              }[e.kind] || e.kind,
              e.manual_schedulable == null
                ? "—"
                : e.manual_schedulable
                  ? t("admin.upstreamSites.enabled")
                  : t("admin.upstreamSites.disabled"),
            ]),
        ),
        h("h3", t("admin.upstreamSites.alertHistory")),
        table(
          [
            t("admin.upstreamSites.time"),
            t("admin.upstreamSites.type"),
            t("admin.upstreamSites.value"),
            t("admin.upstreamSites.notification"),
          ],
          alerts.value.map((a) => [
            displayTime(a.at || a.created_at),
            a.kind,
            String(a.value ?? a.data?.value ?? ""),
            a.delivery || a.delivery_status || t("admin.upstreamSites.saved"),
          ]),
        ),
      ]);
    }
    let refreshTimer;
    onMounted(() => {
      perform(refresh);
      refreshTimer = window.setInterval(() => {
        if (tab.value === "sites" || tab.value === "history")
          perform(async () => {
            await refresh();
            if (tab.value === "history" && selected.value)
              await loadDetails(selected.value, offset.value);
          });
      }, 30000);
    });
    onUnmounted(() => window.clearInterval(refreshTimer));
    const view = () =>
      h("div", { class: "us-page" }, [
        h("div", { class: "us-header" }, [
          h("div", [
            h("h1", t("admin.upstreamSites.upstreamSites")),
            h(
              "p",
              t("admin.upstreamSites.siteCounts", {
                sites: sites.value.length,
                accounts: sites.value.reduce((n, s) => n + s.provider_count, 0),
              }),
            ),
          ]),
          h(SiteBadge, { tone: "neutral" }, () =>
            t("admin.upstreamSites.controlCounts", {
              sites: sites.value.filter((s) => s.config.policy.mode !== "off")
                .length,
              accounts: control.value.status?.holds?.length || 0,
            }),
          ),
        ]),
        h("div", { class: "us-actions" }, [
          button(t("admin.upstreamSites.siteList"), async () => {
            tab.value = "sites";
            await refresh();
          }),
          button(t("admin.upstreamSites.accountBindings"), loadBindings),
        ]),
        error.value
          ? h("div", { class: "us-error", role: "alert" }, error.value)
          : null,
        message.value
          ? h("div", { class: "us-notice", role: "status" }, message.value)
          : null,
        loading.value
          ? h("p", { role: "status" }, t("admin.upstreamSites.working"))
          : null,
        tab.value === "edit" && form.value
          ? editor()
          : tab.value === "bindings"
            ? bindingPage()
            : tab.value === "history" && selected.value
              ? details()
              : overview(),
      ]);
    return () =>
      h(AppLayout, null, {
        default: () => [view(), h(TotpStepUpDialog, { controller: stepUp })],
      });
  },
};
</script>
<style>
.us-page {
  --us-text: #111827;
  --us-secondary: #6b7280;
  --us-border: #e5e7eb;
  --us-surface: #fff;
  --us-muted: #f9fafb;
}
.dark .us-page {
  --us-text: #f3f4f6;
  --us-secondary: #9ca3af;
  --us-border: #374151;
  --us-surface: #111827;
  --us-muted: #1f2937;
}

.us-page {
  color: var(--us-text);
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 8px;
}
.us-page h1 {
  font-size: 24px;
  font-weight: 700;
}
.us-page h2 {
  font-size: 20px;
  font-weight: 600;
  margin: 8px 0 16px;
}
.us-page h3 {
  font-size: 17px;
  font-weight: 600;
  margin: 24px 0 10px;
}
.us-page p {
  margin: 6px 0 12px;
}
.us-header,
.us-actions,
.us-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}
.us-header {
  justify-content: space-between;
}
.us-page .us-actions {
  margin: 10px 0;
}
.us-page td .us-actions {
  min-width: 120px;
  margin: 0;
  gap: 6px;
}
.us-page button {
  white-space: nowrap;
}
.us-page .us-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: 16px;
}
.us-page .us-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin: 8px 0;
  flex: 1;
  min-width: 180px;
}
.us-page input:not([type="checkbox"]),
.us-page textarea,
.us-page select {
  border: 1px solid var(--us-border);
  border-radius: 8px;
  padding: 9px 12px;
  background: var(--us-surface);
  color: var(--us-text);
  font: inherit;
  width: 100%;
}
.us-page input:focus,
.us-page textarea:focus,
.us-page select:focus {
  outline: 2px solid #3b82f6;
  outline-offset: 1px;
}
.us-page .us-actions > input {
  width: 280px;
}
.us-page .us-check {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 14px 0;
}
.us-page fieldset {
  border: 1px solid var(--us-border);
  border-radius: 10px;
  padding: 14px;
  margin: 16px 0;
}
.us-page legend {
  padding: 0 6px;
  font-weight: 600;
}
.us-page .us-scroll {
  overflow: auto;
}
.us-page table {
  border-collapse: collapse;
  width: 100%;
  min-width: 700px;
}
.us-page th,
.us-page td {
  text-align: left;
  padding: 12px;
  border-bottom: 1px solid var(--us-border);
  vertical-align: top;
}
.us-page th {
  background: var(--us-muted);
  white-space: normal;
}
.us-page .us-url {
  overflow-wrap: anywhere;
  max-width: 340px;
  font-size: 13px;
  color: var(--us-secondary);
}
.us-page .us-muted,
.us-page small {
  color: var(--us-secondary);
  font-size: 13px;
}
.us-page .us-error {
  border: 1px solid #dc2626;
  color: #dc2626;
  padding: 12px;
  border-radius: 8px;
}
.us-page .us-notice {
  border: 1px solid var(--us-border);
  background: var(--us-muted);
  padding: 12px;
  border-radius: 8px;
}
.us-page .us-probe + .us-probe {
  border-top: 1px solid var(--us-border);
  padding-top: 12px;
}
.us-control {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 8px;
}
.us-auto-state {
  display: flex;
  align-items: center;
  gap: 6px;
  white-space: nowrap;
}
.us-state-dot {
  display: inline-block;
  width: 9px;
  height: 9px;
  border-radius: 50%;
  flex: none;
}
.us-state-blocked {
  background: #ef4444;
}
.us-state-clear {
  background: #22c55e;
}
.us-state-off {
  background: #9ca3af;
}
@media (max-width: 700px) {
  .us-page {
    padding: 0;
  }
  .us-page .us-grid {
    grid-template-columns: 1fr;
  }
  .us-page .us-row {
    align-items: stretch;
  }
}
</style>
