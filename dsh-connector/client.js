window.__ModuleLoader__.load({
  id: 'todolist-agent-dsh-connector',
  factory(require) {
    const React = require('react');
    const { Button } = require('@deepseek-ai/dsh-client-ui-primitives');
    const h = React.createElement;
    const namespace = 'todolist-agent-connector';
    const messages = {
      zh: {
        provider: '模型后端', model: '模型', effort: '推理强度', inherited: '跟随会话设置',
        adapter: '模型默认', save: '保存', saved: '已保存', loading: '正在读取模型和设置…',
        scope: '这些设置应用于 TodoList Agent 发消息时使用的 DSH 会话。',
        unavailable: '当前配置无法修改此插件的设置。',
        connector: '连接信息与更新', port: '监听地址', rpc: '接口路径', versions: '插件 / DSH / 已验证 DSH',
        limits: '单次事件等待 / 请求上限', state: '服务状态', ready: '运行中', degraded: '服务未就绪',
        check: '检查更新', upgrade: '在线更新', current: '已是最新版本', restart: '已安装，重启 DSH 后生效',
        available: '可更新到', logs: '运行日志', empty: '暂无记录', suggestion: '建议检查插件更新',
        managementUnavailable: '更新和诊断接口暂不可用，请重新打开 DSH 页面。',
        backend: '处理会话', newBackend: '新建并初始化处理会话', selectBackend: '使用此会话',
        initializing: '正在初始化', failed: '初始化失败，请新建会话重试', backendReady: '已就绪',
        backendHint: '已发送的消息继续在原会话核对。后续消息使用这里选择的处理会话。提示词变化后插件自动创建新会话。',
        promptVersion: '初始化提示词指纹',
        workspace: '处理会话工作目录（可选）',
      },
      en: {
        provider: 'Model provider', model: 'Model', effort: 'Reasoning effort', inherited: 'Use session settings',
        adapter: 'Model default', save: 'Save', saved: 'Saved', loading: 'Loading models and settings…',
        scope: 'These settings apply to the DSH session when TodoList Agent sends a message.',
        unavailable: 'This profile does not allow editing the connector settings.',
        connector: 'Connection and updates', port: 'Listening address', rpc: 'RPC path', versions: 'Connector / DSH / tested DSH',
        limits: 'Event wait / request limit', state: 'Service status', ready: 'Running', degraded: 'Service unavailable',
        check: 'Check for updates', upgrade: 'Update online', current: 'Up to date', restart: 'Installed. Restart DSH to apply.',
        available: 'Update available', logs: 'Runtime log', empty: 'No entries', suggestion: 'Check for connector updates',
        managementUnavailable: 'Update and diagnostics are unavailable. Reopen the DSH page.',
        backend: 'Processing session', newBackend: 'Create and initialize a new session', selectBackend: 'Use this session',
        initializing: 'Initializing', failed: 'Initialization failed. Create a new session to retry.', backendReady: 'Ready',
        backendHint: 'Sent messages keep their original session. Later messages use the selected backend. A changed prompt creates a new session.',
        promptVersion: 'Initialization prompt fingerprint',
        workspace: 'Backend working directory (optional)',
      },
    };

    async function manage(action = '', body) {
      const config = window.__TodoListConnectorManagement;
      if (!config) throw new Error('Connector management unavailable');
      const response = await fetch(config.endpoint + (action ? '/' + action : ''), {
        method: action ? 'POST' : 'GET', headers: { Authorization: 'Bearer ' + config.token },
        ...(body ? { body: JSON.stringify(body) } : {}),
      });
      if (response.status === 401) throw new Error('Connector management token expired');
      const result = await response.json();
      if (!response.ok) throw new Error(result.error);
      return result;
    }

    function Management({ t }) {
      const [info, setInfo] = React.useState(null);
      const [busy, setBusy] = React.useState(false);
      const [notice, setNotice] = React.useState('');
      React.useEffect(() => {
        let mounted = true;
        const refresh = () => manage().then(result => { if (mounted) setInfo(result); })
          .catch(() => { if (mounted) setInfo(null); });
        refresh();
        const timer = setInterval(refresh, 5000);
        return () => { mounted = false; clearInterval(timer); };
      }, []);
      async function update(install) {
        setBusy(true);
        setNotice('');
        try {
          const { available } = await manage('check');
          if (!available) setNotice(t('current'));
          else if (!install) setNotice(t('available') + ' ' + available);
          else {
            await manage('install');
            setNotice(t('restart'));
          }
          setInfo(await manage());
        } catch (error) {
          setNotice(error.message);
          await manage().then(setInfo).catch(() => {});
        } finally {
          setBusy(false);
        }
      }
      async function changeBackend(clientID, sessionId) {
        setBusy(true);
        setNotice('');
        try {
          await manage('backend', { clientID, ...(sessionId ? { sessionId } : {}) });
          setInfo(await manage());
        } catch (error) {
          setNotice(error.message);
        } finally {
          setBusy(false);
        }
      }
      const entries = info ? [
        [t('port'), info.host + ':' + info.port],
        [t('rpc'), info.route],
        [t('versions'), [info.version, info.runtime || '—', info.testedDSH].join(' / ')],
        [t('limits'), (info.watchTimeout / 1000) + ' s / ' + (info.maxRequest / 1024 / 1024) + ' MiB'],
        [t('state'), info.listening && info.sessionAPI ? t('ready') : t('degraded')],
      ] : [];
      return h('section', { style: { marginBlock: 16 } },
        h('h3', null, t('connector')),
        info ? h('dl', null, entries.map(([label, value]) =>
          h('div', { key: label, style: { display: 'flex', gap: 12, flexWrap: 'wrap' } },
            h('dt', null, label), h('dd', { style: { margin: 0 } }, value))))
          : h('p', { role: 'status' }, t('managementUnavailable')),
        h('div', { style: { display: 'flex', gap: 8, flexWrap: 'wrap' } },
          h(Button, { disabled: busy || !info, onClick: () => update(false) }, t('check')),
          h(Button, { variant: 'primary', disabled: busy || !info, onClick: () => update(true) }, t('upgrade'))),
        h('p', { role: 'status' }, notice || (info?.staged ? t('restart') : '')),
        info?.backends && h('section', null,
          h('h3', null, t('backend')),
          h('p', null, t('backendHint')),
          h('p', null, t('promptVersion') + ' ' + info.backends.promptHash.slice(0, 12)),
          info.backends.clients.map(client => h('div', { key: client.id, style: { marginBlock: 12 } },
            h('p', null, client.id),
            client.backends.map(backend => h('div', { key: backend.sessionId, style: { marginBlock: 8 } },
              h('span', null, (backend.sessionId === client.active ? '● ' : '') + backend.sessionId + ' · ' +
                (backend.phase === 'ready' ? t('backendReady') : backend.phase === 'failed' ? t('failed') : t('initializing'))),
              backend.compatible && backend.sessionId !== client.active &&
                h(Button, { disabled: busy, onClick: () => changeBackend(client.id, backend.sessionId) }, t('selectBackend')))),
            h(Button, { disabled: busy, onClick: () => changeBackend(client.id) }, t('newBackend'))))),
        info && (!info.listening || !info.sessionAPI || (info.runtime && info.runtime !== info.testedDSH) ||
          info.diagnostics.some(entry => entry.level === 'error')) && h('p', null, t('suggestion')),
        info && h('details', { open: true }, h('summary', null, t('logs')),
          h('pre', { style: { maxHeight: 180, overflow: 'auto', whiteSpace: 'pre-wrap' } },
            info.diagnostics.map(entry => new Date(entry.at).toLocaleTimeString() + '  ' + entry.level + '  ' + entry.code).join('\n') || t('empty'))));
    }

    function Form({ remote, t }) {
      const [form, setForm] = React.useState(null);
      const [notice, setNotice] = React.useState('');
      const [saving, setSaving] = React.useState(false);
      const formID = React.useId();
      React.useEffect(() => {
        let mounted = true;
        Promise.all([remote.settings.describe(), remote.session.modelCatalog()]).then(([settings, catalog]) => {
          if (!settings.ok) throw new Error(settings.error.message);
          if (!catalog.ok) throw new Error(catalog.error.message);
          const entry = settings.value.namespaces.find(item => item.ns === namespace);
          if (!mounted) return;
          if (!entry) { setNotice(t('unavailable')); return; }
          const { provider = '', model = '', reasoningEffort = '', workspace = '' } = entry.value;
          setForm({ provider, model, reasoningEffort, workspace, revision: entry.revision,
            groups: catalog.value.groups, writable: settings.value.writable });
        }).catch(error => { if (mounted) setNotice(error.message); });
        return () => { mounted = false; };
      }, [remote, t]);

      if (!form) return h('p', { role: 'status' }, notice || t('loading'));
      const models = form.groups.find(group => group.id === form.provider)?.models ?? [];
      const efforts = models.find(model => model.id === form.model)?.reasoning?.efforts ?? [];
      const disabled = saving || !form.writable;
      const set = fields => { setNotice(''); setForm({ ...form, ...fields }); };
      function select(label, value, options, change, inactive = false) {
        const labelID = formID + '-' + label;
        return h('label', { style: { display: 'grid', gap: 6 } },
          h('span', { id: labelID }, t(label)),
          h('select', { value, onChange: event => change(event.target.value), disabled: disabled || inactive,
            'aria-labelledby': labelID,
            style: { minHeight: 34, background: 'transparent', color: 'inherit', border: '1px solid currentColor', borderRadius: 6 } },
          options.map(([id, name]) => h('option', { key: id, value: id }, name))));
      }
      async function save(event) {
        event.preventDefault();
        setSaving(true);
        setNotice('');
        const { provider, model, reasoningEffort, workspace, revision } = form;
        try {
          const result = await remote.settings.update(namespace, { provider, model, reasoningEffort, workspace }, revision);
          if (!result.ok) throw new Error(result.error.message);
          setForm({ ...form, revision: result.value.revision });
          setNotice(t('saved'));
        } catch (error) {
          setNotice(error.message);
        } finally {
          setSaving(false);
        }
      }
      return h('form', { onSubmit: save, style: { display: 'grid', gap: 12, maxWidth: 480 } },
        h('p', null, t('scope')),
        h('label', { style: { display: 'grid', gap: 6 } }, h('span', null, t('workspace')),
          h('input', { value: form.workspace, disabled, onChange: event => set({ workspace: event.target.value }),
            style: { minHeight: 34, color: 'inherit', background: 'transparent', border: '1px solid currentColor', borderRadius: 6 } })),
        select('provider', form.provider, [['', t('inherited')], ...form.groups.map(group => [group.id, group.name])],
          provider => set({ provider, model: '', reasoningEffort: '' })),
        select('model', form.model, [['', '—'], ...models.map(model => [model.id, model.name])],
          model => set({ model, reasoningEffort: '' }), !form.provider),
        select('effort', form.reasoningEffort, [['', t('adapter')], ...efforts.map(effort => [effort.id, effort.name])],
          reasoningEffort => set({ reasoningEffort }), efforts.length === 0),
        h('div', null, h(Button, { type: 'submit', variant: 'primary',
          disabled: disabled || (!!form.provider && !models.some(m => m.id === form.model)) }, t('save'))),
        h('p', { role: 'status' }, notice));
    }

    return {
      inject: ['slots', 'locale'],
      apply(ctx) {
        let remote;
        ctx.inject(['remote', 'remote.settings', 'remote.session'], child => { remote = child.remote; });
        ctx.effect(() => ctx.locale.register(namespace, messages));
        ctx.slots.inject('plugins.bundle.config', () => ctx.slots.register({
          name: 'plugins.bundle.config', key: 'todolist-agent-dsh-connector', locale: namespace,
        }, ({ t }) => h(React.Fragment, null, h(Management, { t }), remote && h(Form, { remote, t }))));
      },
    };
  },
});
