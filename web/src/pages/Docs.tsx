import { useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { fromMicros } from '../api'
import { useAuth } from '../auth'
import { CopyButton, ErrorText, InlineCopy, Segmented, Table, useLoad } from '../components/ui'
import type { Model } from '../types'

// /docs: the user-facing API reference. Copy is verbatim from "Tollgate API Docs.dc.html";
// the base URL comes from this page's origin and "Your models" from the console API.

type Lang = 'curl' | 'python' | 'node'
const LANGS: { value: Lang; label: string }[] = [
  { value: 'curl', label: 'curl' },
  { value: 'python', label: 'Python' },
  { value: 'node', label: 'Node.js' },
]
const LANG_KEY = 'tollgate.docs.lang'

const storedLang = (): Lang => {
  try {
    const v = localStorage.getItem(LANG_KEY)
    return v === 'curl' || v === 'python' || v === 'node' ? v : 'python'
  } catch {
    return 'python'
  }
}

const code = (V1: string): Record<Lang, Record<'quick' | 'models' | 'chat' | 'stream' | 'embed' | 'retry', string>> => ({
  curl: {
    quick: `curl ${V1}/chat/completions \\
  -H "Authorization: Bearer $TOLLGATE_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "gpt-4.1",
    "messages": [{"role": "user", "content": "Hello"}]
  }'`,
    models: `curl ${V1}/models \\
  -H "Authorization: Bearer $TOLLGATE_API_KEY"`,
    chat: `curl -i ${V1}/chat/completions \\
  -H "Authorization: Bearer $TOLLGATE_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "claude-sonnet",
    "messages": [
      {"role": "system", "content": "Be brief."},
      {"role": "user", "content": "Name three rivers."}
    ],
    "temperature": 0.2,
    "max_tokens": 200
  }'
# → X-Tollgate-Cost: 0.00456`,
    stream: `curl -N ${V1}/chat/completions \\
  -H "Authorization: Bearer $TOLLGATE_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "gemini-flash",
    "stream": true,
    "stream_options": {"include_usage": true},
    "messages": [{"role": "user", "content": "Count to 3"}]
  }'`,
    embed: `curl ${V1}/embeddings \\
  -H "Authorization: Bearer $TOLLGATE_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "text-embedding-3-small",
    "input": ["tollgate", "gateway"]
  }'`,
    retry: `# Read Retry-After (seconds) and try again
curl -s -D - -o /dev/null ${V1}/chat/completions \\
  -H "Authorization: Bearer $TOLLGATE_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"gpt-4.1","messages":[{"role":"user","content":"hi"}]}' \\
  | grep -i retry-after`,
  },
  python: {
    quick: `import os
from openai import OpenAI

client = OpenAI(
    base_url="${V1}",
    api_key=os.environ["TOLLGATE_API_KEY"],
)

resp = client.chat.completions.create(
    model="gpt-4.1",
    messages=[{"role": "user", "content": "Hello"}],
)
print(resp.choices[0].message.content)`,
    models: `for m in client.models.list():
    print(m.id, m.owned_by)`,
    chat: `raw = client.chat.completions.with_raw_response.create(
    model="claude-sonnet",
    messages=[
        {"role": "system", "content": "Be brief."},
        {"role": "user", "content": "Name three rivers."},
    ],
    temperature=0.2,
    max_tokens=200,
)
resp = raw.parse()
print(resp.choices[0].message.content)
print("cost $", raw.headers["x-tollgate-cost"])`,
    stream: `stream = client.chat.completions.create(
    model="gemini-flash",
    messages=[{"role": "user", "content": "Count to 3"}],
    stream=True,
    stream_options={"include_usage": True},
)
for chunk in stream:
    if chunk.choices:
        print(chunk.choices[0].delta.content or "", end="")
    if chunk.usage:
        print("\\n", chunk.usage.total_tokens, "tokens")`,
    embed: `res = client.embeddings.create(
    model="text-embedding-3-small",
    input=["tollgate", "gateway"],
)
print(len(res.data[0].embedding))`,
    retry: `import time, openai

def chat(**kw):
    for _ in range(3):
        try:
            return client.chat.completions.create(**kw)
        except openai.RateLimitError as e:
            body = e.body or {}
            if body.get("code") == "budget_exceeded":
                raise  # waiting won't help
            time.sleep(int(e.response.headers.get("retry-after", "5")))
    raise RuntimeError("still rate limited")`,
  },
  node: {
    quick: `import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "${V1}",
  apiKey: process.env.TOLLGATE_API_KEY,
});

const resp = await client.chat.completions.create({
  model: "gpt-4.1",
  messages: [{ role: "user", content: "Hello" }],
});
console.log(resp.choices[0].message.content);`,
    models: `for await (const m of client.models.list()) {
  console.log(m.id, m.owned_by);
}`,
    chat: `const { data, response } = await client.chat.completions
  .create({
    model: "claude-sonnet",
    messages: [
      { role: "system", content: "Be brief." },
      { role: "user", content: "Name three rivers." },
    ],
    temperature: 0.2,
    max_tokens: 200,
  })
  .withResponse();

console.log(data.choices[0].message.content);
console.log("cost $", response.headers.get("x-tollgate-cost"));`,
    stream: `const stream = await client.chat.completions.create({
  model: "gemini-flash",
  messages: [{ role: "user", content: "Count to 3" }],
  stream: true,
  stream_options: { include_usage: true },
});
for await (const chunk of stream) {
  process.stdout.write(chunk.choices[0]?.delta?.content ?? "");
  if (chunk.usage) console.log("\\n", chunk.usage.total_tokens, "tokens");
}`,
    embed: `const res = await client.embeddings.create({
  model: "text-embedding-3-small",
  input: ["tollgate", "gateway"],
});
console.log(res.data[0].embedding.length);`,
    retry: `async function chat(body) {
  for (let i = 0; i < 3; i++) {
    try {
      return await client.chat.completions.create(body);
    } catch (err) {
      if (err.status !== 429 || err.code === "budget_exceeded") throw err;
      const s = Number(err.headers?.["retry-after"] ?? 5);
      await new Promise((r) => setTimeout(r, s * 1000));
    }
  }
  throw new Error("still rate limited");
}`,
  },
})

const MODELS_RESP = `{
  "object": "list",
  "data": [
    {"id": "gpt-4.1", "object": "model", "created": 1791000000, "owned_by": "openai"},
    {"id": "claude-sonnet", "object": "model", "created": 1791000000, "owned_by": "anthropic"}
  ]
}`
const CHAT_RESP = `{
  "id": "chatcmpl-…",
  "object": "chat.completion",
  "model": "claude-sonnet",
  "choices": [{
    "index": 0,
    "message": {"role": "assistant", "content": "Nile, Amazon, Yangtze."},
    "finish_reason": "stop"
  }],
  "usage": {"prompt_tokens": 22, "completion_tokens": 9, "total_tokens": 31}
}`
const STREAM_RESP = `data: {"choices":[{"index":0,"delta":{"content":"1"}}]}

data: {"choices":[{"index":0,"delta":{"content":", 2, 3"}}]}

data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":6,"total_tokens":11}}

data: [DONE]`
const ERR_RESP = `Retry-After: 23

{"error": {
  "message": "key RPM limit 60 exceeded",
  "type": "rate_limit_error",
  "code": "rate_limit_exceeded"
}}`
const HEALTH_RESP = `{"postgres": "ok", "redis": "ok"}
{"postgres": "ok", "redis": "down"}`

const CHAT_PARAMS = [
  { name: 'model', type: 'string', req: true, desc: 'Tollgate model name from Your models. 404 model_not_found if unknown or disabled; 403 model_not_allowed if your key, user or group excludes it.' },
  { name: 'messages', type: 'array', req: true, desc: 'Roles system, user, assistant. content is a string or parts of type text / image_url. Google accepts only base64 data: image URLs.' },
  { name: 'stream', type: 'boolean', desc: 'Server-sent events. See Streaming.' },
  { name: 'stream_options.include_usage', type: 'boolean', desc: 'Adds a final usage chunk to the stream.' },
  { name: 'temperature · top_p', type: 'number', desc: 'Sampling. Mapped for every provider.' },
  { name: 'max_tokens · max_completion_tokens', type: 'integer', desc: 'Output cap. Anthropic defaults to 4096 when omitted.' },
  { name: 'stop', type: 'string | string[]', desc: 'Stop sequences. Mapped for every provider.' },
  { name: 'tools · functions', type: 'array', desc: 'OpenAI-compatible models only. Anthropic and Google return 400 unsupported.' },
]
const COMPAT = [
  { name: 'Chat, non-streaming', o: '✓', a: '✓', g: '✓' },
  { name: 'Streaming + include_usage', o: '✓', a: '✓', g: '✓' },
  { name: 'System messages', o: '✓', a: '✓ (as system)', g: '✓ (as system instruction)' },
  { name: 'Images (image_url)', o: '✓ any URL', a: '✓ data: or http(s) URL', g: 'data: URLs only' },
  { name: 'Tools / function calling', o: '✓ passthrough', a: '— 400 unsupported', g: '— 400 unsupported' },
  { name: 'Other OpenAI params', o: 'passed through', a: 'dropped', g: 'dropped' },
  { name: 'Embeddings', o: '✓', a: '— 400 unsupported', g: '— 400 unsupported' },
  { name: 'Prompt caching (billed at cache rate)', o: 'cache read', a: 'cache read + write', g: 'cache read' },
]
const ERRORS = [
  { s: '400', code: 'invalid_request', desc: 'Malformed JSON or missing model/messages. Fix the request.', retry: 'No' },
  { s: '400', code: 'unsupported', desc: 'Feature not available for this provider (tools, embeddings, non-data: images on Google). See Provider support.', retry: 'No' },
  { s: '401', code: 'invalid_api_key', desc: 'Missing or unknown key.', retry: 'No' },
  { s: '403', code: 'key_blocked', desc: 'Key revoked or owner disabled. Ask an admin.', retry: 'No' },
  { s: '403', code: 'key_expired', desc: 'Key past expiry. Rotate it.', retry: 'No' },
  { s: '403', code: 'model_not_allowed', desc: "Model isn't allowed for this key, its owner or the owner's group.", retry: 'No' },
  { s: '404', code: 'model_not_found', desc: 'Unknown or disabled model name.', retry: 'No' },
  { s: '404', code: 'not_found', desc: 'No such endpoint (e.g. GET /v1/models/{id}).', retry: 'No' },
  { s: '413', code: 'request_too_large', desc: 'Body over 20 MiB.', retry: 'No' },
  { s: '429', code: 'rate_limit_exceeded', desc: 'Key or group RPM/TPM hit. Wait Retry-After seconds.', retry: 'Yes' },
  { s: '429', code: 'budget_exceeded', desc: 'Key or group budget used up (type insufficient_quota). Blocked until a daily/monthly budget resets or an admin raises it.', retry: 'No' },
  { s: '503', code: 'rate_limiter_unavailable', desc: "Tollgate can't check limits right now; fails closed.", retry: 'Backoff' },
  { s: '4xx/5xx', code: 'upstream_error', desc: "Provider's status and message, or 502 if the provider was unreachable or returned an invalid response. Charged only for usage the provider reported (usually $0).", retry: '5xx only' },
]

const NAV = [
  ['quickstart', 'Quickstart'],
  ['auth', 'Authentication'],
  ['models', 'Your models'],
  ['chat', 'Chat completions'],
  ['streaming', 'Streaming'],
  ['embeddings', 'Embeddings'],
  ['compat', 'Provider support'],
  ['limits', 'Limits & billing'],
  ['errors', 'Errors'],
  ['health', 'Health'],
]

// Small pieces used throughout the page.
const C = ({ children, className = '' }: { children: ReactNode; className?: string }) => <code className={`font-mono text-xs ${className}`}>{children}</code>
const Chip = ({ children }: { children: ReactNode }) => <code className="bg-secondary px-1.5 py-px font-mono text-xs">{children}</code>
const A = ({ to, children }: { to: string; children: ReactNode }) =>
  to.startsWith('#') ? (
    <a href={to} className="text-link-foreground underline underline-offset-2 hover:text-foreground">{children}</a>
  ) : (
    <Link to={to} className="text-link-foreground underline underline-offset-2 hover:text-foreground">{children}</Link>
  )
const B = ({ children }: { children: ReactNode }) => <b className="font-medium text-foreground">{children}</b>
const H2 = ({ children }: { children: ReactNode }) => <h2 className="text-xl font-semibold">{children}</h2>
const Method = ({ m, path, tone = 'post' }: { m: string; path: string; tone?: 'post' | 'get' }) => (
  <div className="flex items-center gap-2.5">
    <span className={`px-2 py-0.5 font-mono text-xs font-semibold ${tone === 'get' ? 'bg-success text-success-foreground' : 'bg-primary-highlight text-primary-highlight-foreground'}`}>{m}</span>
    <code className="font-mono text-sm">{path}</code>
  </div>
)

// Two-column section: prose left, code right; stacks below ~800px.
function Section({ id, children, last = false, first = false }: { id: string; children: ReactNode; last?: boolean; first?: boolean }) {
  return (
    <section
      id={id}
      className={`grid scroll-mt-8 grid-cols-[repeat(auto-fit,minmax(min(100%,380px),1fr))] gap-x-10 gap-y-6 ${first ? 'pb-10' : 'py-10'} ${last ? '' : 'border-b border-border'}`}
    >
      {children}
    </section>
  )
}

function CodePanel({ title, text, response, responseLabel, stretch = false }: { title: string; text: string; response?: string; responseLabel?: string; stretch?: boolean }) {
  return (
    <div className={`flex min-w-0 flex-col border border-border bg-card ${stretch ? '' : 'self-start'}`}>
      <div className="flex h-9 items-center justify-between border-b border-border pl-3 pr-2">
        <span className="text-xs text-muted-foreground">{title}</span>
        <CopyButton value={text} ariaLabel={`Copy ${title} code`} />
      </div>
      <pre className="overflow-x-auto px-4 py-3.5 font-mono text-[12.5px] leading-[1.6]">{text}</pre>
      {response && (
        <>
          <div className="flex h-8 items-center border-t border-border px-3 text-xs text-muted-foreground">{responseLabel}</div>
          <pre className="overflow-x-auto px-4 py-3.5 font-mono text-xs leading-[1.6] text-muted-foreground">{response}</pre>
        </>
      )}
    </div>
  )
}

function Param({ name, type, req, children }: { name: string; type: string; req?: boolean; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5 border-b border-border py-2.5">
      <div className="flex flex-wrap items-baseline gap-2">
        <code className="font-mono text-[13px] font-semibold">{name}</code>
        <span className="text-xs text-muted-foreground">{type}</span>
        {req && <span className="text-[11px] font-medium text-warning-foreground">required</span>}
      </div>
      <span className="text-[13px] text-muted-foreground">{children}</span>
    </div>
  )
}

export default function Docs() {
  const { can } = useAuth()
  const origin = window.location.origin
  const V1 = origin + '/v1'
  const [lang, setLangState] = useState<Lang>(storedLang)
  const setLang = (l: Lang) => {
    setLangState(l)
    try {
      localStorage.setItem(LANG_KEY, l)
    } catch {
      // storage blocked: the choice lasts for this page view
    }
  }
  const c = code(V1)[lang]
  const langName = LANGS.find((l) => l.value === lang)!.label
  // Enabled models only. Embeddings are an OpenAI-compatible-only endpoint.
  const models = useLoad<Model[]>('/console/api/v1/models')
  const enabled = (models.data ?? []).filter((m) => m.enabled)

  return (
    <div className="leading-[1.55]">
      <div className="mb-2 flex flex-wrap items-start justify-between gap-4">
        <h1 className="text-2xl font-semibold leading-[1.2]">API reference</h1>
        <div className="flex flex-wrap items-center gap-3">
          <InlineCopy label="Base URL" value={V1} />
          <Segmented tall label="Code language" value={lang} onChange={setLang} options={LANGS} />
        </div>
      </div>
      <p className="mb-8 max-w-[720px] text-muted-foreground">
        Tollgate speaks the OpenAI API. Point any OpenAI SDK or tool at the base URL above, use a Tollgate key, and pick a model from the list below.
      </p>

      <div className="flex items-start gap-10">
        <nav aria-label="On this page" className="sticky top-8 hidden w-[168px] shrink-0 flex-col gap-0.5 text-[13px] lg:flex">
          <span className="px-2 pb-2 text-xs font-semibold uppercase tracking-[0.04em] text-muted-foreground">On this page</span>
          {NAV.map(([id, label]) => (
            <a key={id} href={`#${id}`} className="px-2 py-1 text-foreground hover:bg-accent">{label}</a>
          ))}
        </nav>

        <div className="flex min-w-0 flex-1 flex-col">
          <Section id="quickstart" first>
            <div className="flex flex-col gap-3">
              <H2>Quickstart</H2>
              <ol className="flex list-decimal flex-col gap-2 pl-5">
                {can('keys:write_own') ? (
                  <li>Create a key on <A to="/keys">API keys</A>. Copy it — it's shown once.</li>
                ) : (
                  <li>Ask an admin for an API key. It's shown once.</li>
                )}
                <li>Export it: <Chip>export TOLLGATE_API_KEY=tg-…</Chip></li>
                <li>Set your SDK's base URL to <Chip>{V1}</Chip> and call it like OpenAI.</li>
              </ol>
              <p className="text-muted-foreground">
                Model names are Tollgate's, not the provider's — use the <B>Name</B> column in <A to="#models">Your models</A>.
              </p>
            </div>
            <CodePanel stretch title={langName} text={c.quick} />
          </Section>

          <Section id="auth">
            <div className="flex flex-col gap-3">
              <H2>Authentication</H2>
              <p>Send your key as a bearer token on every <C>/v1</C> request.</p>
              <code className="bg-secondary px-3 py-2.5 font-mono text-xs text-secondary-foreground">Authorization: Bearer tg-…</code>
              <div className="flex flex-col border border-border text-[13px]">
                {[
                  ['401', 'invalid_api_key', 'Header missing, or key unknown.'],
                  ['403', 'key_blocked', 'Key revoked, or its owner was disabled.'],
                  ['403', 'key_expired', "Past the key's expiry. Rotate it on API keys."],
                ].map(([s, code, desc], i, all) => (
                  <div key={code} className={`grid grid-cols-[56px_140px_1fr] gap-3 px-3 py-2 ${i < all.length - 1 ? 'border-b border-border' : ''}`}>
                    <code className="font-mono">{s}</code>
                    <code className="font-mono text-xs">{code}</code>
                    <span className="text-muted-foreground">{desc}</span>
                  </div>
                ))}
              </div>
              <p className="text-muted-foreground">Revocation is immediate. Keep keys server-side — never ship them in a browser or mobile app.</p>
            </div>
            <CodePanel stretch title={`${langName} · list models`} text={c.models} response={MODELS_RESP} responseLabel="Response · 200" />
          </Section>

          <section id="models" className="flex scroll-mt-8 flex-col gap-3 border-b border-border py-10">
            <H2>Your models</H2>
            <p className="max-w-[720px] text-muted-foreground">
              Enabled models you can call. A key with an <i>Allowed models</i> list sees only those — <C>GET /v1/models</C> with that key is the source of truth.
            </p>
            <ErrorText>{models.error}</ErrorText>
            <Table
              rows={models.data ? enabled : models.error ? [] : null}
              empty="No models are enabled yet."
              cols={[
                { head: 'Name', cell: (m) => <code className="font-mono text-[13px] font-medium">{m.name}</code> },
                { head: 'Provider', cell: (m) => m.provider },
                { head: 'Endpoints', cell: (m) => <code className="font-mono text-xs text-muted-foreground">{m.provider === 'openai' ? 'chat · embeddings' : 'chat'}</code> },
                { head: '$ / 1M in · out', cell: (m) => `${fromMicros(m.price_input_micros)} · ${fromMicros(m.price_output_micros)}` },
                { head: '', cell: (m) => <CopyButton size="row" value={m.name} label="Copy name" ariaLabel={`Copy model name ${m.name}`} /> },
              ]}
            />
          </section>

          <Section id="chat">
            <div className="flex flex-col gap-3">
              <Method m="POST" path="/v1/chat/completions" />
              <H2>Chat completions</H2>
              <p>
                Same request and response as OpenAI. Tollgate swaps <C>model</C> for the provider's ID and translates for Anthropic and Google.
              </p>
              <div className="flex flex-col border-t border-border">
                {CHAT_PARAMS.map((p) => (
                  <Param key={p.name} name={p.name} type={p.type} req={p.req}>{p.desc}</Param>
                ))}
              </div>
              <p className="text-[13px] text-muted-foreground">
                Other OpenAI parameters pass through untouched to OpenAI-compatible models and are dropped for Anthropic and Google. Request body max 20 MiB (413{' '}
                <code className="font-mono">request_too_large</code>). Requests time out after 10 minutes.
              </p>
              <div className="flex flex-col gap-1 border border-border p-3">
                <span className="text-[13px] font-medium">Response header</span>
                <span className="text-[13px] text-muted-foreground">
                  <code className="font-mono text-foreground">X-Tollgate-Cost: 0.00456</code> — USD for this request. Non-streaming only; for streams, see Dashboard.
                </span>
              </div>
            </div>
            <CodePanel title={`${langName} · chat + read cost`} text={c.chat} response={CHAT_RESP} responseLabel="Response · 200" />
          </Section>

          <Section id="streaming">
            <div className="flex flex-col gap-3">
              <H2>Streaming</H2>
              <p>
                Set <C>"stream": true</C> to get server-sent events. Each event is <C>data: {'{chunk}'}</C>; the stream ends with <C>data: [DONE]</C>.
              </p>
              <ul className="flex list-disc flex-col gap-1.5 pl-5 text-muted-foreground">
                <li>
                  Want token counts? Add <C className="text-foreground">"stream_options": {'{"include_usage": true}'}</C> — the last chunk before{' '}
                  <C className="text-foreground">[DONE]</C> carries <C className="text-foreground">usage</C>. Works for every provider.
                </li>
                <li>
                  If the provider fails mid-stream, you get one more event with an <C className="text-foreground">error</C> object (
                  <C className="text-foreground">upstream_error</C>) and no <C className="text-foreground">[DONE]</C>. The HTTP status is already 200.
                </li>
                <li>Disconnecting doesn't cancel billing: Tollgate finishes reading the provider's stream and bills exact usage.</li>
                <li>No <C className="text-foreground">X-Tollgate-Cost</C> header on streams.</li>
              </ul>
            </div>
            <CodePanel title={`${langName} · stream`} text={c.stream} response={STREAM_RESP} responseLabel="Events" />
          </Section>

          <Section id="embeddings">
            <div className="flex flex-col gap-3">
              <Method m="POST" path="/v1/embeddings" />
              <H2>Embeddings</H2>
              <p>
                OpenAI's embeddings request, forwarded as-is. <b className="font-medium">OpenAI-compatible models only</b> — Anthropic and Google models return 400 <C>unsupported</C>.
              </p>
              <div className="flex flex-col border-t border-border">
                <Param name="model" type="string" req>A model whose endpoints include <code className="font-mono">embeddings</code>.</Param>
                <Param name="input" type="string | string[]" req>
                  Text to embed. Other OpenAI fields (<code className="font-mono">dimensions</code>, <code className="font-mono">encoding_format</code>) pass through.
                </Param>
              </div>
              <p className="text-[13px] text-muted-foreground">Returns <code className="font-mono">X-Tollgate-Cost</code>.</p>
            </div>
            <CodePanel title={`${langName} · embed`} text={c.embed} />
          </Section>

          <section id="compat" className="flex scroll-mt-8 flex-col gap-3 border-b border-border py-10">
            <H2>Provider support</H2>
            <p className="max-w-[720px] text-muted-foreground">
              One API, three provider families. Check a model's <B>Provider</B> in <A to="#models">Your models</A>.
            </p>
            <div className="overflow-x-auto border border-border">
              <div className="grid h-10 min-w-[600px] grid-cols-[1.6fr_1fr_1fr_1fr] items-center gap-3 border-b border-border bg-table-header px-3 text-[13px] font-medium text-muted-foreground">
                <span>Feature</span>
                <span>OpenAI-compatible</span>
                <span>Anthropic</span>
                <span>Google</span>
              </div>
              {COMPAT.map((f) => (
                <div key={f.name} className="grid min-h-10 min-w-[600px] grid-cols-[1.6fr_1fr_1fr_1fr] items-center gap-3 border-b border-border px-3 py-2 text-[13px] last:border-b-0">
                  <span>{f.name}</span>
                  <span>{f.o}</span>
                  <span className="text-muted-foreground">{f.a}</span>
                  <span className="text-muted-foreground">{f.g}</span>
                </div>
              ))}
            </div>
            <p className="text-[13px] text-muted-foreground">
              Not available: <code className="font-mono">/v1/responses</code>, <code className="font-mono">/v1/messages</code>, <code className="font-mono">GET /v1/models/{'{id}'}</code>, images, audio, fine-tuning.
            </p>
          </section>

          <section id="limits" className="flex scroll-mt-8 flex-col gap-3 border-b border-border py-10">
            <H2>Limits &amp; billing</H2>
            <div className="grid grid-cols-[repeat(auto-fit,minmax(min(100%,260px),1fr))] gap-px border border-border bg-border">
              {[
                ['Two layers', <>Every request is checked against your <B>key</B> and its <B>group</B>. The first limit hit wins; the message says which.</>],
                ['RPM / TPM', <>Fixed one-minute windows on the clock minute. Tokens count after the response (input + output). On 429 wait <code className="font-mono">Retry-After</code> seconds — the time to the next minute.</>],
                ['Budgets', <>Daily or monthly budgets reset at midnight / the 1st in the server timezone; total budgets never reset. Budgets are checked when a request starts and its cost is added when it finishes, so requests already in flight can push spend past the budget.</>],
                ["What's billed", <>Every request that reached the provider is logged and charged for the tokens it reported, so an upstream error usually costs $0. Requests Tollgate rejects cost nothing. <code className="font-mono">prompt_tokens</code> includes cached tokens.</>],
              ].map(([title, body]) => (
                <div key={title as string} className="flex flex-col gap-1.5 bg-background p-4">
                  <strong className="font-semibold">{title}</strong>
                  <span className="text-[13px] text-muted-foreground">{body}</span>
                </div>
              ))}
            </div>
            <p className="text-[13px] text-muted-foreground">
              Your limits are on <A to="/keys">API keys</A> (expand a key) and <A to="/people/groups">Groups</A>. Spend is on <A to="/">Dashboard</A>.
            </p>
          </section>

          <Section id="errors">
            <div className="flex min-w-0 flex-col gap-3">
              <H2>Errors</H2>
              <p>
                OpenAI's error shape: <C>{'{"error": {"message", "type", "code"}}'}</C>. Branch on <C>code</C>, not <C>message</C>.
              </p>
              <div className="overflow-x-auto border border-border">
                <div className="grid h-9 min-w-[520px] grid-cols-[48px_170px_1fr_64px] items-center gap-2.5 border-b border-border bg-table-header px-3 text-xs font-medium text-muted-foreground">
                  <span>HTTP</span>
                  <span>code</span>
                  <span>Meaning / what to do</span>
                  <span>Retry</span>
                </div>
                {ERRORS.map((e) => (
                  <div key={e.code} className="grid min-w-[520px] grid-cols-[48px_170px_1fr_64px] items-baseline gap-2.5 border-b border-border px-3 py-2 text-[13px] last:border-b-0">
                    <code className="font-mono">{e.s}</code>
                    <code className="font-mono text-xs">{e.code}</code>
                    <span className="text-muted-foreground">{e.desc}</span>
                    <span className="text-xs">{e.retry}</span>
                  </div>
                ))}
              </div>
            </div>
            <CodePanel title={`${langName} · retry on 429`} text={c.retry} response={ERR_RESP} responseLabel="Response · 429" />
          </Section>

          <Section id="health" last>
            <div className="flex flex-col gap-3">
              <Method m="GET" path="/health" tone="get" />
              <H2>Health</H2>
              <p>
                No key needed. <b className="font-medium">200</b> when Postgres and Redis respond within 2 s, <b className="font-medium">503</b> otherwise. Use it for load
                balancer and uptime checks — it doesn't test providers.
              </p>
            </div>
            <CodePanel title="curl" text={`curl ${origin}/health`} response={HEALTH_RESP} responseLabel="Response · 200 / 503" />
          </Section>
        </div>
      </div>
    </div>
  )
}
