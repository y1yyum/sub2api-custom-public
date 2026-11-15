import { mount, flushPromises } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import UpstreamSitesView from "../UpstreamSitesView.vue";
import i18n, { loadLocaleMessages } from "@/i18n";

const request = vi.hoisted(() => vi.fn());
vi.mock("@/api/client", () => ({ apiClient: { request } }));
vi.mock("@/components/layout/AppLayout.vue", () => ({
  default: { template: "<main><slot /></main>" },
}));
vi.mock("@/components/auth/TotpStepUpDialog.vue", () => ({
  default: { template: "<div />" },
}));
vi.mock("@/composables/useStepUp", () => ({
  useStepUp: () => ({ run: (work: () => Promise<unknown>) => work() }),
  isStepUpCancelled: () => false,
}));

const config = () => ({
  name: "Example",
  notes: "",
  base_url: "https://example.com",
  adapter: "sub2api",
  endpoints: [],
  auto_query: false,
  interval_minutes: 60,
  timezone: "UTC",
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
});
const site = (id = 1) => ({
  id,
  config_version: 1,
  provider_count: 0,
  config: config(),
  state: { manual: false, reasons: {}, version: 1 },
  archived: false,
  has_key: true,
});

beforeEach(async () => {
  await loadLocaleMessages("zh");
  i18n.global.locale.value = "zh";
  request.mockReset();
  request.mockImplementation(async ({ url }) => {
    if (url.endsWith("/sites")) return { data: { sites: [site()] } };
    if (url.endsWith("/control"))
      return { data: { available: true, status: { holds: [] } } };
    if (url.endsWith("/providers"))
      return {
        data: [
          {
            id: 42,
            name: "Account",
            base_url: "https://example.com",
            status: "active",
            schedulable: false,
          },
        ],
      };
    if (url.endsWith("/bindings"))
      return { data: { bindings: {}, version: 0 } };
    return { data: {} };
  });
});

async function click(wrapper: ReturnType<typeof mount>, label: string) {
  const button = wrapper.findAll("button").find((b) => b.text() === label);
  expect(button, label).toBeTruthy();
  await button!.trigger("click");
  await flushPromises();
}

describe("上游站点管理", () => {
  it("已有阈值可编辑，新增阈值可保存", async () => {
    const current = site();
    current.config.policy.mode = "balance";
    const thresholds = [{ unit: "USD", low: "1", recover: "5" }];
    const original = request.getMockImplementation()!;
    request.mockImplementation(async (r) =>
      r.url.endsWith("/sites") && r.method === "GET"
        ? {
            data: {
              sites: [
                {
                  ...current,
                  config: {
                    ...current.config,
                    policy: { ...current.config.policy, thresholds },
                  },
                },
              ],
            },
          }
        : original(r),
    );
    const errors: unknown[] = [];
    const wrapper = mount(UpstreamSitesView, {
      global: { config: { errorHandler: (error) => errors.push(error) } },
    });
    try {
      await flushPromises();
      await click(wrapper, "编辑");
      expect(errors).toEqual([]);
      const low = wrapper
        .findAll("label")
        .find((l) => l.text() === "停用阈值")!;
      await low.find("input").setValue("2");
      await click(wrapper, "添加阈值");
      expect(errors).toEqual([]);
      expect(
        wrapper.findAll("label").filter((l) => l.text() === "停用阈值"),
      ).toHaveLength(2);
      const thresholdRows = wrapper
        .findAll(".us-row")
        .filter((row) => row.text().includes("停用阈值"));
      await thresholdRows[1].find("select").setValue("CNY");
      await click(wrapper, "保存");
      const write = request.mock.calls
        .map(([r]) => r)
        .find((r) => r.method === "PUT");
      expect(write.data.config.policy.thresholds).toEqual([
        { unit: "USD", low: "2", recover: "5" },
        { unit: "CNY", low: "1", recover: "5" },
      ]);
    } finally {
      wrapper.unmount();
    }
  });

  it("编辑探针草稿后保留唯一名称，移除未保存的字段", async () => {
    const probe = (id: string) => ({
      id,
      url: "https://example.com/health",
      interval_minutes: 5,
      timeout_seconds: 10,
      json_path: "success",
      expected: "true",
      use_wallet_key: false,
      success_codes: [200],
      required: false,
    });
    const current = site();
    const original = request.getMockImplementation()!;
    request.mockImplementation(async (r) =>
      r.url.endsWith("/sites") && r.method === "GET"
        ? {
            data: {
              sites: [
                {
                  ...current,
                  config: {
                    ...current.config,
                    probes: [probe("probe-1"), probe("probe-2")],
                  },
                },
              ],
            },
          }
        : original(r),
    );
    const wrapper = mount(UpstreamSitesView);
    try {
      await flushPromises();
      await click(wrapper, "编辑");
      const first = wrapper.findAll(".us-probe")[0];
      await first.find('input[type="password"]').setValue("draft-value");
      await first.find("input").setValue("renamed");
      await first.find("button").trigger("click");
      await flushPromises();
      await click(wrapper, "添加探针");
      await click(wrapper, "保存");
      const write = request.mock.calls
        .map(([r]) => r)
        .find((r) => r.method === "PUT");
      expect(write.data.config.probes.map((p: { id: string }) => p.id)).toEqual(
        ["probe-2", "probe-1"],
      );
      expect(write.data.secrets.probe_keys).toEqual({});
    } finally {
      wrapper.unmount();
    }
  });

  it("人工按钮独立显示状态，启用不清除自动限制", async () => {
    const current = site();
    current.state.manual = true;
    current.state.reasons = { balance_low: "2026-11-15T00:00:00Z" };
    current.config.policy.mode = "balance";
    const original = request.getMockImplementation()!;
    request.mockImplementation(async (r) => {
      if (r.url.endsWith("/sites")) return { data: { sites: [current] } };
      if (r.url.endsWith("/manual")) {
        current.state.manual = !r.data.enabled;
        current.state.version++;
        return { data: current.state };
      }
      return original(r);
    });
    const wrapper = mount(UpstreamSitesView);
    await flushPromises();
    expect(wrapper.find('[role="status"]').text()).toBe("自动：已限制");
    expect(wrapper.find('[aria-pressed="false"]').text()).toBe("人工：已禁用");
    await click(wrapper, "人工：已禁用");
    expect(
      request.mock.calls.find(([r]) => r.method === "PUT")![0],
    ).toMatchObject({
      url: "/admin/upstream-sites/sites/1/manual",
      data: { enabled: true, state_version: 1 },
    });
    expect(wrapper.find('[aria-pressed="true"]').text()).toBe("人工：已启用");
    expect(wrapper.find('[role="status"]').text()).toBe("自动：已限制");
    expect(wrapper.text()).toContain("站点限制中");
    await click(wrapper, "人工：已启用");
    current.state.reasons = {};
    await click(wrapper, "刷新");
    expect(wrapper.find('[role="status"]').text()).toBe("自动：未限制");
    expect(wrapper.find('[aria-pressed="false"]').text()).toBe("人工：已禁用");
    expect(wrapper.text()).toContain("站点限制中");
    await click(wrapper, "人工：已禁用");
    expect(wrapper.text()).toContain("站点正常");
    wrapper.unmount();
  });

  it("每次手动查询刷新余额差值成本，定时刷新只读取已保存统计", async () => {
    vi.useFakeTimers();
    const current = {
      ...site(),
      costs: [{ unit: "USD", amount: "2.25", gap: false }],
      today: [{ unit: "USD", upstream_cost: "2.25" }],
    };
    const original = request.getMockImplementation()!;
    request.mockImplementation(async (r) => {
      if (r.url.endsWith("/sites")) return { data: { sites: [current] } };
      if (r.url.endsWith("/query")) {
        current.costs[0].amount = "4";
        current.today[0].upstream_cost = "4";
        return { data: { outcome: "healthy" } };
      }
      return original(r);
    });
    const wrapper = mount(UpstreamSitesView);
    try {
      await flushPromises();
      expect(wrapper.text()).toContain("今日：2.25 USD");
      await click(wrapper, "查询");
      expect(wrapper.text()).toContain("今日：4 USD");
      current.costs[0].amount = "5.1";
      current.today[0].upstream_cost = "5.1";
      request.mockClear();
      await vi.advanceTimersByTimeAsync(30000);
      await flushPromises();
      expect(wrapper.text()).toContain("今日：5.1 USD");
      expect(request.mock.calls.every(([r]) => r.method === "GET")).toBe(true);
    } finally {
      wrapper.unmount();
      expect(vi.getTimerCount()).toBe(0);
      vi.useRealTimers();
    }
  });

  it("已删除账号的残留绑定可在草稿中清理，保存前不写入", async () => {
    const original = request.getMockImplementation()!;
    request.mockImplementation(async (r) =>
      r.url.endsWith("/bindings") && r.method === "GET"
        ? { data: { bindings: { 99: 1 }, version: 7 } }
        : original(r),
    );
    const wrapper = mount(UpstreamSitesView);
    await flushPromises();
    await click(wrapper, "账号绑定");
    expect(wrapper.text()).toContain("已清理失效账号，保存以确认。");
    expect(request.mock.calls.every(([r]) => r.method === "GET")).toBe(true);
    await click(wrapper, "保存绑定");
    expect(
      request.mock.calls.find(([r]) => r.method === "PUT")![0].data,
    ).toEqual({ bindings: {}, version: 7 });
    wrapper.unmount();
  });
  it("新建默认关闭控制、恢复和自动查询，保存不携带已有秘密", async () => {
    const wrapper = mount(UpstreamSitesView);
    await flushPromises();
    await click(wrapper, "新增站点");
    const labels = wrapper.findAll("label");
    await labels
      .find((l) => l.text() === "名称")!
      .find("input")
      .setValue("New site");
    await labels
      .find((l) => l.text() === "余额查询 URL")!
      .find("input")
      .setValue("https://new.example.com");
    await click(wrapper, "保存");
    const write = request.mock.calls
      .map(([r]) => r)
      .find((r) => r.method === "POST");
    expect(write.data.config.policy).toMatchObject({
      mode: "off",
      auto_recover: false,
    });
    expect(write.data.config.auto_query).toBe(false);
    expect(write.data.config.raw_retention_days).toBe(90);
    expect(write.data.secrets.key).toBe("");
    wrapper.unmount();
  });

  it("地址扫描仅建议，显式保存才修改绑定，不修改账号", async () => {
    const wrapper = mount(UpstreamSitesView);
    await flushPromises();
    await click(wrapper, "账号绑定");
    await click(wrapper, "建议绑定");
    expect(request.mock.calls.every(([r]) => r.method === "GET")).toBe(true);
    expect(wrapper.text()).toContain("未保存修改");
    await click(wrapper, "保存绑定");
    const writes = request.mock.calls
      .map(([r]) => r)
      .filter((r) => r.method !== "GET");
    expect(writes).toHaveLength(1);
    expect(writes[0]).toMatchObject({
      method: "PUT",
      url: "/admin/upstream-sites/bindings",
      data: { bindings: { 42: 1 }, version: 0 },
    });
    wrapper.unmount();
  });

  it("同地址多个钱包不自动选择", async () => {
    const original = request.getMockImplementation()!;
    request.mockImplementation(async (r) =>
      r.url.endsWith("/sites")
        ? { data: { sites: [site(1), site(2)] } }
        : original(r),
    );
    const wrapper = mount(UpstreamSitesView);
    await flushPromises();
    await click(wrapper, "账号绑定");
    await click(wrapper, "建议绑定");
    expect(wrapper.text()).toContain("已匹配 0 个建议绑定");
    expect(wrapper.find("select").element.value).toBe("");
    wrapper.unmount();
  });

  it("编辑保留凭据，取消不修改已保存配置", async () => {
    const wrapper = mount(UpstreamSitesView);
    await flushPromises();
    await click(wrapper, "编辑");
    expect(
      wrapper
        .findAll("input[type=password]")
        .every((i) => i.element.value === ""),
    ).toBe(true);
    const name = wrapper.findAll("label").find((l) => l.text() === "名称")!;
    await name.find("input").setValue("Unsaved");
    await click(wrapper, "取消");
    expect(wrapper.text()).toContain("Example");
    expect(wrapper.text()).not.toContain("Unsaved");
    expect(request.mock.calls.every(([r]) => r.method === "GET")).toBe(true);
    wrapper.unmount();
  });
});
