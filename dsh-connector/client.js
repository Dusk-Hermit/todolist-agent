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
      },
      en: {
        provider: 'Model provider', model: 'Model', effort: 'Reasoning effort', inherited: 'Use session settings',
        adapter: 'Model default', save: 'Save', saved: 'Saved', loading: 'Loading models and settings…',
        scope: 'These settings apply to the DSH session when TodoList Agent sends a message.',
        unavailable: 'This profile does not allow editing the connector settings.',
      },
    };

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
          const { provider = '', model = '', reasoningEffort = '' } = entry.value;
          setForm({ provider, model, reasoningEffort, revision: entry.revision,
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
        const { provider, model, reasoningEffort, revision } = form;
        try {
          const result = await remote.settings.update(namespace, { provider, model, reasoningEffort }, revision);
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
      inject: ['slots', 'locale', 'remote', 'remote.settings', 'remote.session'],
      apply(ctx) {
        ctx.effect(() => ctx.locale.register(namespace, messages));
        ctx.slots.inject('plugins.bundle.config', () => ctx.slots.register({
          name: 'plugins.bundle.config', key: 'todolist-agent-dsh-connector', locale: namespace,
        }, ({ t }) => h(Form, { remote: ctx.remote, t })));
      },
    };
  },
});
