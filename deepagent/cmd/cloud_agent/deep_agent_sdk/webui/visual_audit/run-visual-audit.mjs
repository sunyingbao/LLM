import { existsSync } from "node:fs";
import { mkdir, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { delimiter, dirname, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { installMockApp, visualBaseURL, visualScenarios } from "./fixtures.mjs";

const useMock = process.env.VISUAL_AUDIT_MOCK !== "0";
const baseURL = visualBaseURL(useMock);
const staticRoot = resolve(process.cwd(), "deepagent/cmd/cloud_agent/deep_agent_sdk/webui/static");
const scenarioFilter = process.env.SCENARIO_FILTER || "";
const mockScenarios = scenarioFilter
  ? visualScenarios.filter((scenario) => scenario.name.includes(scenarioFilter))
  : visualScenarios;
const liveScenarios = [{
  name: "live-workspace",
  viewport: { name: "desktop", width: 1536, height: 1024 },
  path: "/",
  steps: [
    { kind: "assertExists", selector: "[data-sidebar]" },
    { kind: "assertExists", selector: "[data-workspace]" },
    { kind: "assertExists", selector: "[data-inspector]" },
    { kind: "assertExists", selector: "[data-run-status]" },
    { kind: "assertAriaTab", selectedTab: "changes" },
    { kind: "assertStyle", selector: "body", props: { fontFamily: "-apple-system", fontSize: "13px" } },
  ],
}];
const scenarios = useMock ? mockScenarios : liveScenarios;

const selectors = {
  tokenContract: [
    "--font-body: 13px",
    "--font-input: 14px",
    "--font-meta: 12px",
    "--font-code: 12px",
    "--line-body: 20px",
    "--line-input: 21px",
    "--line-meta: 16px",
    "--line-code: 18px",
  ],
};

const requireFromMeta = createRequire(import.meta.url);

function playwrightAPI(moduleToLoad) {
  if (moduleToLoad?.chromium) {
    return moduleToLoad;
  }
  if (moduleToLoad?.default?.chromium) {
    return moduleToLoad.default;
  }
  return null;
}

function buildModuleRoots() {
  const roots = new Set();
  roots.add(process.cwd());
  roots.add(resolve(dirname(process.execPath), "..", "lib"));

  if (process.env.NPM_CONFIG_PREFIX) {
    roots.add(resolve(process.env.NPM_CONFIG_PREFIX, "lib", "node_modules"));
  }
  if (process.env.npm_config_prefix) {
    roots.add(resolve(process.env.npm_config_prefix, "lib", "node_modules"));
  }
  const nodePath = process.env.NODE_PATH;
  if (nodePath) {
    for (const rawPath of nodePath.split(delimiter)) {
      const value = rawPath?.trim();
      if (!value) {
        continue;
      }
      roots.add(dirname(resolve(value)));
    }
  }

  return [...roots];
}

async function tryImportResolvedPath(candidatePath) {
  try {
    const resolvedPath = requireFromMeta.resolve(candidatePath);
    return await import(pathToFileURL(resolvedPath).href);
  } catch {
    return null;
  }
}

async function tryImportPlaywrightFromRoots() {
  const requireRoots = buildModuleRoots();
  for (const moduleName of ["playwright", "playwright-core"]) {
    for (const basePath of requireRoots) {
      try {
        const resolvedPath = requireFromMeta.resolve(moduleName, { paths: [basePath] });
        const moduleToLoad = await import(pathToFileURL(resolvedPath).href);
        const api = playwrightAPI(moduleToLoad);
        if (api) {
          return api;
        }
      } catch {
        continue;
      }
    }
  }
  return null;
}

async function assertServerReachable(url) {
  const checkURL = `${url.replace(/\/$/, "")}/`;
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 4000);
  try {
    const response = await fetch(checkURL, {
      method: "GET",
      signal: controller.signal,
    });
    return response.status >= 200 && response.status < 600;
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    console.log(`[visual-audit] server unreachable at ${checkURL}: ${message}`);
    return false;
  } finally {
    clearTimeout(timeout);
  }
}

async function tryLoadPlaywright() {
  if (process.env.PLAYWRIGHT_MODULE_PATH) {
    const rawPath = process.env.PLAYWRIGHT_MODULE_PATH.trim();
    const moduleToLoad = existsSync(rawPath) ? await tryImportResolvedPath(rawPath) : null;
    const api = playwrightAPI(moduleToLoad);
    if (api) {
      return api;
    }
    console.log(`[visual-audit] PLAYWRIGHT_MODULE_PATH ignored (${rawPath})`);
  }

  const moduleToLoad = await tryImportPlaywrightFromRoots();
  if (moduleToLoad) {
    return moduleToLoad;
  }

  const prefix = process.env.VISUAL_AUDIT_STRICT === "1" ? "failed" : "skip";
  console.log(`[visual-audit] ${prefix}: playwright/playwright-core module not available`);
  console.log("[visual-audit] set PLAYWRIGHT_MODULE_PATH=<module> or install playwright first");
  return null;
}

function normalizeText(value) {
  return String(value || "").replaceAll(/\s+/g, " ").trim();
}

async function runStep(page, step) {
  if (step.kind === "openInspectorIfCollapsed") {
    const collapsed = await page.locator("[data-inspector]").getAttribute("data-collapsed");
    if (collapsed !== "true") return { ok: true };
    const selector = page.viewportSize()?.width < 900 ? "[data-open-inspector]" : "[data-collapse-inspector]";
    await page.click(selector);
    return { ok: true };
  }

  if (step.kind === "click") {
    await page.click(step.selector);
    return { ok: true };
  }

  if (step.kind === "hover") {
    await page.locator(step.selector).hover();
    return { ok: true };
  }

  if (step.kind === "press") {
    await page.locator(step.selector).press(step.key);
    return { ok: true };
  }

  if (step.kind === "fill") {
    await page.locator(step.selector).fill(step.value);
    return { ok: true };
  }

  if (step.kind === "check") {
    await page.locator(step.selector).first().check();
    return { ok: true };
  }

  if (step.kind === "assertExists") {
    const count = await page.locator(step.selector).count();
    return { ok: count > 0, detail: `selector=${step.selector}, count=${count}` };
  }

  if (step.kind === "assertAbsent") {
    const count = await page.locator(step.selector).count();
    return { ok: count === 0, detail: `selector=${step.selector}, count=${count}` };
  }

  if (step.kind === "assertHidden") {
    const hidden = await page.locator(step.selector).isHidden();
    return { ok: hidden, detail: `selector=${step.selector} hidden=${hidden}` };
  }

  if (step.kind === "assertAttribute") {
    const value = await page.locator(step.selector).getAttribute(step.key || "");
    return {
      ok: value === step.value,
      detail: `selector=${step.selector} ${step.key}=${JSON.stringify(value)} expect=${JSON.stringify(step.value)}`,
    };
  }

  if (step.kind === "assertTextPresent") {
    const text = normalizeText(await page.textContent(step.selector));
    return {
      ok: step.assertValue(text),
      detail: `selector=${step.selector} text=${JSON.stringify(text)}`,
    };
  }

  if (step.kind === "assertTextIncludes") {
    await page.waitForFunction(({ selector, value }) => {
      const content = document.querySelector(selector)?.textContent || "";
      const actual = content.replace(/\s+/g, " ").trim();
      const expected = String(value).replace(/\s+/g, " ").trim();
      return actual.includes(expected);
    }, { selector: step.selector, value: step.value }, { timeout: 5000 });
    const text = normalizeText(await page.locator(step.selector).textContent());
    return {
      ok: text.includes(normalizeText(step.value)),
      detail: `selector=${step.selector} text=${JSON.stringify(text)} includes=${JSON.stringify(step.value)}`,
    };
  }

  if (step.kind === "assertWidth") {
    const box = await page.locator(step.selector).boundingBox();
    const actual = Math.round(box?.width || 0);
    return { ok: actual === step.value, detail: `selector=${step.selector} width=${actual} expect=${step.value}` };
  }

  if (step.kind === "assertAriaTab") {
    const tabs = ["changes", "files", "terminal"];
    for (const tab of tabs) {
      const locator = page.locator(`[data-inspector-tab='${tab}']`);
      const count = await locator.count();
      if (!count) {
        return { ok: false, detail: `tab selector missing: ${tab}` };
      }
      const actual = await locator.getAttribute("aria-selected");
      const expected = tab === step.selectedTab ? "true" : "false";
      if (actual !== expected) {
        return {
          ok: false,
          detail: `aria-selected ${tab}=${JSON.stringify(actual)} expect=${JSON.stringify(expected)}`,
        };
      }
    }
    return { ok: true, detail: `selectedTab=${step.selectedTab}` };
  }

  if (step.kind === "assertStyle") {
    const ok = await page.locator(step.selector).evaluate((node, props) => {
      const style = window.getComputedStyle(node);
      return Object.entries(props).every(([key, value]) => {
        const computed = String(style.getPropertyValue(key) || style[key] || "");
        const expected = Array.isArray(value) ? value : [value];
        return expected.some((item) => computed.includes(item));
      });
    }, step.props);
    return { ok, detail: `selector=${step.selector}` };
  }

  if (step.kind === "assertStorage") {
    const value = await page.evaluate((key) => {
      try {
        return globalThis.localStorage?.getItem(key) || "";
      } catch {
        return "";
      }
    }, step.key);
    return {
      ok: value === step.value,
      detail: `localStorage[${step.key}]=${JSON.stringify(value)} expect=${JSON.stringify(step.value)}`,
    };
  }

  return { ok: false, detail: `Unknown step kind ${step.kind}` };
}

async function assertTokens(page) {
  const tokensSource = await page.evaluate(async () => {
    const response = await fetch("/static/styles/tokens.css");
    return response.text();
  });
  const missing = [];
  for (const token of selectors.tokenContract) {
    if (!tokensSource.includes(token)) {
      missing.push(token);
    }
  }
  return {
    ok: missing.length === 0,
    detail: missing.length ? `missing=${missing.join(", ")}` : "tokens.ok",
    missing,
  };
}

async function runScenario(browser, scenario, report) {
  const viewport = scenario.viewport;
  const context = await browser.newContext({
    viewport: { width: viewport.width, height: viewport.height },
    reducedMotion: "reduce",
    locale: "en-US",
    timezoneId: "Asia/Shanghai",
  });
  if (useMock) await installMockApp(context, scenario.fixture || {}, staticRoot);
  const page = await context.newPage();
  const path = scenario.path || "/";
  const entry = {
    name: scenario.name,
    viewport,
    path,
    steps: [],
    passed: true,
    error: "",
    pageErrors: [],
    url: `${baseURL}${path}`,
  };
  report.results.push(entry);
  page.on("pageerror", (error) => entry.pageErrors.push(String(error?.message || error)));
  try {
    await page.goto(`${baseURL}${path}`, { waitUntil: "domcontentloaded" });
    await page.locator("[data-run-status]").waitFor();

    for (const step of scenario.steps) {
      const result = await runStep(page, step);
      entry.steps.push({ ...step, ...result });
      if (!result.ok) {
        entry.passed = false;
        entry.error = result.detail;
        break;
      }
    }

    const tokenCheck = await assertTokens(page);
    entry.steps.push({ kind: "assertTokens", ...tokenCheck });
    if (!tokenCheck.ok) {
      entry.passed = false;
      entry.error = tokenCheck.detail;
    }

    if (entry.pageErrors.length) {
      entry.passed = false;
      entry.error = `page errors: ${entry.pageErrors.join(" | ")}`;
    }

    if (entry.passed || !process.env.FAIL_FAST) {
      const shotDir = join(process.cwd(), "deepagent/cmd/cloud_agent/deep_agent_sdk/webui/visual_audit/.reports", report.id);
      await mkdir(shotDir, { recursive: true });
      const shotPath = join(shotDir, `${scenario.name}.png`);
      await page.screenshot({ path: shotPath, fullPage: true });
      entry.screenshot = shotPath.replace(process.cwd() + "/", "");
    }
  } catch (error) {
    entry.passed = false;
    entry.error = String(error?.message || error);
  } finally {
    await context.close();
  }
  return entry.passed;
}

async function main() {
  const pw = await tryLoadPlaywright();
  if (!pw) {
    if (process.env.VISUAL_AUDIT_STRICT === "1") {
      process.exitCode = 2;
    }
    return;
  }

  const serverOK = useMock || await assertServerReachable(baseURL);
  if (!serverOK) {
    console.log("[visual-audit] skip: backend/frontend is not reachable");
    console.log("[visual-audit] start API service first (example: DEEP_AGENT_SDK_API_ADDRESS=:8080 go run ...)");
    if (process.env.VISUAL_AUDIT_STRICT === "1") {
      process.exitCode = 3;
    }
    return;
  }

  const report = {
    id: new Date().toISOString().replace(/[.:]/g, "-").replace(/T/, "_"),
    baseURL,
    mockBackend: useMock,
    startedAt: new Date().toISOString(),
    viewportCount: new Set(scenarios.map((scenario) => scenario.viewport.name)).size,
    scenarioCount: scenarios.length,
    results: [],
  };

  const browser = await pw.chromium.launch({
    args: ["--disable-dev-shm-usage"],
  });

  try {
    for (const scenario of scenarios) await runScenario(browser, scenario, report);
  } finally {
    await browser.close();
  }

  const passed = report.results.every((item) => item.passed);
  report.finishedAt = new Date().toISOString();
  report.passed = passed;
  const reportPath = join(process.cwd(), "deepagent/cmd/cloud_agent/deep_agent_sdk/webui/visual_audit/.reports", report.id, "visual-summary.json");
  await mkdir(join(process.cwd(), "deepagent/cmd/cloud_agent/deep_agent_sdk/webui/visual_audit/.reports", report.id), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`, "utf8");

  if (passed) {
    console.log("[visual-audit] PASS", reportPath);
    return;
  }
  console.error("[visual-audit] FAIL");
  for (const item of report.results.filter((r) => !r.passed)) {
    console.error(`- ${item.name}/${item.viewport.name}: ${item.error}`);
  }
  process.exitCode = 1;
}

main().catch((error) => {
  console.error("[visual-audit] ERROR", String(error?.message || error));
  process.exit(1);
});
