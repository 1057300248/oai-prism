// Model metadata normalization only. No model-name guessing or inference calls.
export function findPrismModels(value, depth = 0, state = {nodes: 0, found: []}) {
  if (++state.nodes > 10000 || depth > 16) throw new Error('metadata nesting limit');
  if (!value || typeof value !== 'object') return state.found;
  if (Object.hasOwn(value, 'prism_codex_models')) state.found.push(value.prism_codex_models);
  if (value.name === 'prism_codex_models' && Object.hasOwn(value, 'value')) state.found.push(value.value);
  for (const [key, child] of Object.entries(value)) {
    if (key !== 'prism_codex_models' && !(key === 'value' && value.name === 'prism_codex_models')) {
      if (child && typeof child === 'object') findPrismModels(child, depth + 1, state);
    }
  }
  return state.found;
}
export function atPointer(value, pointer) {
  if (!pointer) return value;
  if (!pointer.startsWith('/')) throw new Error('JSON pointer must begin with /');
  for (const part of pointer.slice(1).split('/')) {
    const key = part.replaceAll('~1', '/').replaceAll('~0', '~');
    if (!value || typeof value !== 'object' || !Object.hasOwn(value, key)) throw new Error('metadata pointer missing');
    value = value[key];
  }
  return value;
}
function stringID(v, max = 256) {
  if (typeof v !== 'string' || v.length === 0 || v.length > max || /[\s\x00-\x1f\\"<>?#]/.test(v)) throw new Error('invalid model metadata identifier');
  return v;
}
export function normalizeModels(value, observedAt = new Date()) {
  if (!Array.isArray(value)) value = value?.models ?? value?.data;
  if (!Array.isArray(value) || value.length > 512) throw new Error('expected a bounded model array');
  const seen = new Set();
  const models = value.map(entry => {
    if (typeof entry === 'string') entry = {id: entry};
    if (!entry || typeof entry !== 'object' || Array.isArray(entry)) throw new Error('invalid model entry');
    const id = stringID(entry.id ?? entry.slug ?? entry.model);
    if (seen.has(id)) throw new Error('duplicate model identifier');
    seen.add(id);
    const rawEfforts = entry.reasoning_efforts ?? entry.supported_reasoning_efforts ?? entry.supported_reasoning_levels ?? [];
    if (!Array.isArray(rawEfforts) || rawEfforts.length > 32) throw new Error('invalid effort list');
    const efforts = rawEfforts.map(e => stringID(typeof e === 'string' ? e : e?.effort, 32));
    if (new Set(efforts).size !== efforts.length) throw new Error('duplicate effort');
    const defaultEffort = entry.default_effort ?? entry.default_reasoning_effort ?? entry.default_reasoning_level ?? '';
    if (defaultEffort !== '' && !efforts.includes(defaultEffort)) throw new Error('undeclared default effort');
    const window = entry.context_window ?? 0;
    if (!Number.isSafeInteger(window) || window < 0 || window > 2000000) throw new Error('invalid context window');
    const modalities = entry.input_modalities ?? [];
    if (!Array.isArray(modalities) || modalities.length > 8 || modalities.some(m => !['text', 'image', 'audio'].includes(m))) throw new Error('invalid input modalities');
    const label = entry.display_name ?? entry.label ?? id;
    if (typeof label !== 'string' || label.length > 256) throw new Error('invalid label');
    // Unknown capabilities stay unknown. Never import instructions/permissions,
    // credentials, URLs, model aliases that change identity, or native claims.
    return {id, upstream_id: id, display_name: label, reasoning_efforts: efforts,
      default_effort: defaultEffort, context_window: window, input_modalities: modalities, capabilities: {}};
  });
  return {observed_at: observedAt.toISOString(), models};
}
