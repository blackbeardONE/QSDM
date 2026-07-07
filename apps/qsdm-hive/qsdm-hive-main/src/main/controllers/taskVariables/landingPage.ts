export function getLandingPageContent() {
  return `
    <!doctype html>
    <html lang="en">
      <head>
        <meta charset="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <title>QSDM Task Extension Helper</title>
        <style>
          :root {
            color-scheme: dark;
            --bg: #071f2a;
            --panel: #0d3442;
            --panel-soft: #123f4d;
            --border: rgba(177, 223, 225, 0.26);
            --text: #f3fbfb;
            --muted: #a8cbd0;
            --accent: #67d7d2;
            --ok: #9be7c4;
            --warn: #ffc78f;
            --danger: #ff8f8f;
          }

          * {
            box-sizing: border-box;
          }

          body {
            margin: 0;
            min-height: 100vh;
            background: radial-gradient(circle at top left, #10495c, var(--bg) 45%);
            color: var(--text);
            font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
          }

          main {
            width: min(1120px, calc(100vw - 32px));
            margin: 0 auto;
            padding: 40px 0 56px;
          }

          header {
            display: flex;
            align-items: center;
            gap: 16px;
            margin-bottom: 32px;
          }

          .logo {
            display: grid;
            width: 44px;
            height: 44px;
            place-items: center;
            border-radius: 10px;
            background: #05151d;
            border: 1px solid var(--border);
            color: var(--ok);
            font-size: 26px;
            font-weight: 800;
          }

          h1 {
            margin: 0;
            font-size: 30px;
            line-height: 1.2;
          }

          .subtitle {
            margin: 6px 0 0;
            color: var(--muted);
            line-height: 1.5;
          }

          .notice {
            margin: 0 0 24px;
            padding: 14px 16px;
            border: 1px solid var(--border);
            border-radius: 8px;
            background: rgba(5, 21, 29, 0.55);
            color: var(--muted);
            line-height: 1.5;
          }

          .grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
            gap: 16px;
          }

          .card {
            min-height: 220px;
            padding: 18px;
            border: 1px solid var(--border);
            border-radius: 8px;
            background: rgba(13, 52, 66, 0.88);
          }

          .card.disabled {
            opacity: 0.68;
          }

          .row {
            display: flex;
            align-items: center;
            justify-content: space-between;
            gap: 12px;
            margin-bottom: 10px;
          }

          h2 {
            margin: 0;
            font-size: 18px;
          }

          .status {
            min-width: 74px;
            padding: 4px 8px;
            border: 1px solid var(--border);
            border-radius: 999px;
            color: var(--warn);
            text-align: center;
            font-size: 12px;
            font-weight: 700;
          }

          .status.ok {
            color: #063523;
            border-color: transparent;
            background: var(--ok);
          }

          p {
            margin: 0 0 16px;
            color: var(--muted);
            line-height: 1.45;
          }

          label {
            display: block;
            margin-bottom: 6px;
            color: var(--text);
            font-size: 13px;
            font-weight: 700;
          }

          input {
            width: 100%;
            height: 38px;
            padding: 0 12px;
            border: 1px solid var(--border);
            border-radius: 6px;
            outline: none;
            background: var(--panel-soft);
            color: var(--text);
          }

          input:focus {
            border-color: var(--accent);
          }

          button {
            height: 38px;
            min-width: 110px;
            margin-top: 12px;
            border: 0;
            border-radius: 6px;
            background: var(--accent);
            color: #032026;
            font-weight: 800;
            cursor: pointer;
          }

          button:disabled {
            opacity: 0.55;
            cursor: not-allowed;
          }

          .message {
            min-height: 20px;
            margin-top: 10px;
            color: var(--muted);
            font-size: 13px;
          }

          .message.error {
            color: var(--danger);
          }

          .footer {
            margin-top: 28px;
            color: var(--muted);
            font-size: 13px;
          }
        </style>
      </head>
      <body>
        <main>
          <header>
            <div class="logo">Q</div>
            <div>
              <h1>QSDM Task Extension Helper</h1>
              <p class="subtitle">Save local variables for QSDM Hive tasks. These values stay on this machine.</p>
            </div>
          </header>

          <p class="notice">
            QSDM Core consumes task extension values only when a task explicitly requests and pairs them.
            External add-on packages are disabled in this build; this page only creates or updates local Hive variables.
          </p>

          <section class="grid" id="extension-grid"></section>
          <div class="footer">Close this tab when you are done. QSDM Hive will refresh the saved extension list automatically.</div>
        </main>

        <script>
          const extensionDefinitions = [
            {
              key: 'githubUsername',
              label: 'GITHUB_USERNAME',
              title: 'GitHub Username',
              description: 'Optional public username for tasks that need to attribute repository activity.',
              inputType: 'text',
              placeholder: 'your-github-name'
            },
            {
              key: 'githubToken',
              label: 'GITHUB_TOKEN',
              title: 'GitHub Token',
              description: 'Optional personal access token for tasks that request repository access.',
              inputType: 'password',
              placeholder: 'Paste token'
            },
            {
              key: 'anthropic',
              label: 'ANTHROPIC_API_KEY',
              title: 'Anthropic API Key',
              description: 'Optional key for tasks that request Anthropic model access.',
              inputType: 'password',
              placeholder: 'Paste API key'
            },
            {
              key: 'gemini',
              label: 'GEMINI_API_KEY',
              title: 'Gemini API Key',
              description: 'Optional key for tasks that request Gemini model access.',
              inputType: 'password',
              placeholder: 'Paste API key'
            },
            {
              key: 'grok',
              label: 'GROK_API_KEY',
              title: 'Grok API Key',
              description: 'Not enabled yet in this QSDM Hive build.',
              inputType: 'password',
              placeholder: 'Disabled',
              disabled: true
            }
          ];

          const statusByLabel = {};

          function statusForLabel(label, statusData) {
            if (label === 'GITHUB_USERNAME' || label === 'GITHUB_TOKEN') {
              return statusData.hasGithub ? 'saved' : 'missing';
            }
            if (label === 'ANTHROPIC_API_KEY') return statusData.hasClaude ? 'saved' : 'missing';
            if (label === 'GEMINI_API_KEY') return statusData.hasGemini ? 'saved' : 'missing';
            if (label === 'GROK_API_KEY') return statusData.hasGrok ? 'saved' : 'disabled';
            return 'missing';
          }

          function renderCards(statusData = {}) {
            const grid = document.getElementById('extension-grid');
            grid.innerHTML = '';

            extensionDefinitions.forEach((definition) => {
              const status = definition.disabled ? 'disabled' : statusForLabel(definition.label, statusData);
              statusByLabel[definition.label] = status;

              const card = document.createElement('article');
              card.className = definition.disabled ? 'card disabled' : 'card';

              const row = document.createElement('div');
              row.className = 'row';

              const title = document.createElement('h2');
              title.textContent = definition.title;

              const badge = document.createElement('span');
              badge.className = status === 'saved' ? 'status ok' : 'status';
              badge.textContent = status === 'saved' ? 'saved' : status;

              row.appendChild(title);
              row.appendChild(badge);

              const description = document.createElement('p');
              description.textContent = definition.description;

              const label = document.createElement('label');
              label.setAttribute('for', definition.key);
              label.textContent = definition.label;

              const input = document.createElement('input');
              input.id = definition.key;
              input.type = definition.inputType;
              input.placeholder = definition.placeholder;
              input.disabled = Boolean(definition.disabled);
              input.autocomplete = 'off';

              const button = document.createElement('button');
              button.type = 'button';
              button.textContent = status === 'saved' ? 'Update' : 'Save';
              button.disabled = Boolean(definition.disabled);

              const message = document.createElement('div');
              message.className = 'message';

              button.addEventListener('click', async () => {
                const value = input.value.trim();
                message.className = 'message';
                if (!value) {
                  message.textContent = 'Enter a value first.';
                  message.classList.add('error');
                  return;
                }

                button.disabled = true;
                button.textContent = 'Saving';

                try {
                  const response = await fetch('/api/task-variables-upsert', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ label: definition.label, value })
                  });
                  const data = await response.json();

                  if (!response.ok || !data.success) {
                    throw new Error(data.error || 'Save failed');
                  }

                  input.value = '';
                  message.textContent = data.action === 'updated' ? 'Updated locally.' : 'Saved locally.';
                  await refreshStatus();
                } catch (error) {
                  message.textContent = error instanceof Error ? error.message : 'Save failed';
                  message.classList.add('error');
                } finally {
                  button.disabled = Boolean(definition.disabled);
                  button.textContent = statusByLabel[definition.label] === 'saved' ? 'Update' : 'Save';
                }
              });

              card.appendChild(row);
              card.appendChild(description);
              card.appendChild(label);
              card.appendChild(input);
              card.appendChild(button);
              card.appendChild(message);
              grid.appendChild(card);
            });
          }

          async function refreshStatus() {
            try {
              const response = await fetch('/api/task-variables-check');
              const data = await response.json();
              renderCards(data.success ? data.data : {});
            } catch (error) {
              renderCards({});
            }
          }

          refreshStatus();
        </script>
      </body>
    </html>
  `;
}
