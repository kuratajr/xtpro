(() => {
  const STORAGE_KEY = "xtpro-lang";
  const SUPPORTED = ["vi", "en"];
  const DEFAULT_LANG = "vi";

  /** @type {{ lang: string, dict: any, dictVi: any, loading?: Promise<any> }} */
  const state = {
    lang: DEFAULT_LANG,
    dict: null,
    dictVi: null
  };

  function normalizeLang(input) {
    const raw = String(input || "").toLowerCase();
    if (raw.startsWith("en")) return "en";
    if (raw.startsWith("vi")) return "vi";
    return "";
  }

  function detectLang() {
    const stored = normalizeLang(localStorage.getItem(STORAGE_KEY));
    if (stored && SUPPORTED.includes(stored)) return stored;
    const nav = normalizeLang(navigator.language || "");
    if (nav && SUPPORTED.includes(nav)) return nav;
    return DEFAULT_LANG;
  }

  function getLang() {
    return state.lang;
  }

  function setLang(lang) {
    const next = normalizeLang(lang) || DEFAULT_LANG;
    state.lang = SUPPORTED.includes(next) ? next : DEFAULT_LANG;
    try {
      localStorage.setItem(STORAGE_KEY, state.lang);
    } catch {
      // ignore
    }
    document.documentElement.setAttribute("lang", state.lang);
  }

  function resolveKey(obj, key) {
    if (!obj) return undefined;
    const parts = String(key).split(".");
    let cur = obj;
    for (const p of parts) {
      if (cur && Object.prototype.hasOwnProperty.call(cur, p)) cur = cur[p];
      else return undefined;
    }
    return cur;
  }

  function format(str, vars) {
    if (!vars) return str;
    return String(str).replace(/\{\{(\w+)\}\}/g, (_, k) =>
      Object.prototype.hasOwnProperty.call(vars, k) ? String(vars[k]) : `{{${k}}}`
    );
  }

  function t(key, vars) {
    const v = resolveKey(state.dict, key);
    if (typeof v === "string") return format(v, vars);
    const vi = resolveKey(state.dictVi, key);
    if (typeof vi === "string") return format(vi, vars);
    return String(key);
  }

  async function fetchJson(url) {
    const res = await fetch(url, { cache: "no-store" });
    if (!res.ok) throw new Error(`Failed to load ${url}: ${res.status}`);
    return await res.json();
  }

  async function loadDict(lang) {
    const target = normalizeLang(lang) || DEFAULT_LANG;
    const finalLang = SUPPORTED.includes(target) ? target : DEFAULT_LANG;

    if (state.loading) return state.loading;

    state.loading = (async () => {
      if (!state.dictVi) state.dictVi = await fetchJson(`/assets/i18n/vi.json`);
      if (finalLang === "vi") {
        state.dict = state.dictVi;
        return state.dict;
      }
      state.dict = await fetchJson(`/assets/i18n/${finalLang}.json`);
      return state.dict;
    })().finally(() => {
      state.loading = undefined;
    });

    return state.loading;
  }

  function applyNodeText(el, key, vars, allowHtml) {
    const value = t(key, vars);
    if (allowHtml) el.innerHTML = value;
    else el.textContent = value;
  }

  function applyTranslations(root = document) {
    const nodes = root.querySelectorAll("[data-i18n], [data-i18n-html], [data-i18n-attr]");
    nodes.forEach((el) => {
      const key = el.getAttribute("data-i18n");
      const keyHtml = el.getAttribute("data-i18n-html");
      if (key) applyNodeText(el, key, null, false);
      if (keyHtml) applyNodeText(el, keyHtml, null, true);

      const attrSpec = el.getAttribute("data-i18n-attr");
      if (attrSpec) {
        // format: "placeholder:login.username;title:common.actions.logout"
        attrSpec.split(";").map(s => s.trim()).filter(Boolean).forEach((pair) => {
          const idx = pair.indexOf(":");
          if (idx <= 0) return;
          const attr = pair.slice(0, idx).trim();
          const k = pair.slice(idx + 1).trim();
          if (!attr || !k) return;
          el.setAttribute(attr, t(k));
        });
      }
    });

    // Let pages update any dynamic bits (theme label, etc.)
    window.dispatchEvent(new CustomEvent("xtpro:i18n:applied", { detail: { lang: state.lang } }));
  }

  async function init() {
    setLang(detectLang());
    try {
      await loadDict(state.lang);
      applyTranslations(document);
    } catch (e) {
      console.warn("[i18n] init failed", e);
    }
    window.__xtproI18nReady = true;
    window.dispatchEvent(new CustomEvent("xtpro:i18n:ready", { detail: { lang: state.lang } }));
  }

  // Expose global API
  window.XTProI18n = {
    detectLang,
    getLang,
    setLang,
    loadDict,
    t,
    applyTranslations,
    init
  };

  // Auto-init when loaded
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", () => init().catch(() => {}));
  } else {
    init().catch(() => {});
  }
})();

