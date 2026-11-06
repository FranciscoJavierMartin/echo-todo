// Minimal placeholder SPA: cookie session + CSRF double submit.
const $ = (s) => document.querySelector(s);
const status = (m) => { $("#status").textContent = m; };

async function api(path, opts = {}) {
  const headers = { "Content-Type": "application/json", "X-Client": "browser", ...(opts.headers || {}) };
  if (opts.method && opts.method !== "GET") {
    const r = await fetch("/v1/auth/csrf", { credentials: "same-origin" });
    headers["X-CSRF-Token"] = (await r.json()).csrf_token;
  }
  return fetch(path, { credentials: "same-origin", ...opts, headers });
}

async function loadTodos() {
  const r = await api("/v1/todos?limit=50");
  if (!r.ok) { status("Please sign in"); return; }
  const { items } = await r.json();
  $("#todos").replaceChildren(...items.map((t) => {
    const li = document.createElement("li");
    li.textContent = t.title;
    if (t.status === "done") li.className = "done";
    return li;
  }));
  status(`${items.length} todos`);
}

$("#login").addEventListener("submit", async (e) => {
  e.preventDefault();
  const f = new FormData(e.target);
  const r = await api("/v1/auth/login", { method: "POST", body: JSON.stringify(Object.fromEntries(f)) });
  status(r.ok ? "Signed in" : "Invalid credentials");
  if (r.ok) loadTodos();
});

loadTodos();
