import type {
	IDataObject,
	IExecuteFunctions,
	ILoadOptionsFunctions,
	INodeExecutionData,
	INodePropertyOptions,
	INodeType,
	INodeTypeDescription,
} from 'n8n-workflow';
import { NodeConnectionTypes } from 'n8n-workflow';
import { randomUUID } from 'node:crypto';
import net from 'node:net';

const CRED = 'acpAgentApi';
const DEFAULT_ACP_PORT = 8080;
const JSON_RPC_VERSION = '2.0';
const ACP_VERSION = 1;
const MODEL_CATEGORY = 'model';
const REASONING_CATEGORY = 'thought_level';
const DEFAULT_CWD = '/workspace';
const DEFAULT_TIMEOUT_SECONDS = 120;
const CONFIG_OPTIONS_CACHE_MS = 60_000;

const configOptionsCache = new Map<string, { expiresAt: number; promise: Promise<AcpConfigOption[]> }>();
const sharedConnections = new Map<string, Promise<AcpConnection>>();

export class AcpAgent implements INodeType {
	description: INodeTypeDescription = {
		displayName: 'ACP Agent',
		name: 'acpAgent',
		group: ['transform'],
		version: 1,
		description: 'Run workflow data through an ACP-enabled agent harness',
		defaults: { name: 'ACP Agent' },
		icon: 'node:ai-agent' as unknown as INodeTypeDescription['icon'],
		iconColor: 'black',
		codex: {
			categories: ['AI'],
			subcategories: {
				AI: ['Agents', 'Root Nodes'],
			},
		},
		inputs: `={{ [{ type: '${NodeConnectionTypes.Main}' }, { displayName: 'Tools', type: '${NodeConnectionTypes.AiTool}' }, ...($parameter.hasOutputParser ? [{ displayName: 'Output Parser', maxConnections: 1, type: '${NodeConnectionTypes.AiOutputParser}' }] : [])] }}`,
		outputs: [NodeConnectionTypes.Main],
		builderHint: {
			inputs: {
				ai_tool: {
					required: false,
				},
				ai_outputParser: {
					required: false,
					displayOptions: { show: { hasOutputParser: [true] } },
				},
			},
		},
		credentials: [{ name: CRED, required: true }],
		properties: [
			{
				displayName: 'Prompt Type',
				name: 'promptType',
				type: 'options',
				options: [
					{
						name: 'Connected Chat Trigger Node',
						value: 'auto',
						description: 'Use the chatInput field from each input item',
					},
					{
						name: 'Define Below',
						value: 'define',
						description: 'Use the prompt text configured on this node',
					},
				],
				default: 'define',
			},
			{
				displayName: 'Prompt',
				name: 'prompt',
				type: 'string',
				typeOptions: { rows: 8 },
				default: '',
				required: true,
				description: 'Prompt sent to the ACP runner with each input item',
				displayOptions: { show: { promptType: ['define'] } },
			},
			{
				displayName: 'Model',
				name: 'model',
				type: 'options',
				default: '',
				typeOptions: { loadOptionsMethod: 'getModelOptions' },
				description: 'Model options advertised by the ACP harness for a new session',
			},
			{
				displayName: 'Reasoning Effort',
				name: 'reasoningEffort',
				type: 'options',
				default: '',
				typeOptions: { loadOptionsMethod: 'getReasoningEffortOptions' },
				description: 'Reasoning options advertised by the ACP harness for a new session',
			},
			{
				displayName: 'Require Specific Output Format',
				name: 'hasOutputParser',
				type: 'boolean',
				default: false,
				noDataExpression: true,
			},
			{
				displayName: `Connect an <a data-action='openSelectiveNodeCreator' data-action-parameter-connectiontype='${NodeConnectionTypes.AiOutputParser}'>output parser</a> on the canvas to specify the output format you require`,
				name: 'outputParserNotice',
				type: 'notice',
				default: '',
				displayOptions: { show: { hasOutputParser: [true] } },
			},
			{
				displayName: 'Options',
				name: 'options',
				type: 'collection',
				placeholder: 'Add option',
				default: {},
				options: [
					{
						displayName: 'Timeout Seconds',
						name: 'timeoutSeconds',
						type: 'number',
						typeOptions: { minValue: 1 },
						default: DEFAULT_TIMEOUT_SECONDS,
						description: 'Maximum time the runner should spend on one item',
					},
					{
						displayName: 'Working Directory',
						name: 'cwd',
						type: 'string',
						default: DEFAULT_CWD,
						description: 'Absolute working directory for the ACP session',
					},
				],
			},
		],
	};

	methods = {
		loadOptions: {
			async getModelOptions(this: ILoadOptionsFunctions): Promise<INodePropertyOptions[]> {
				return await configOptionsForCategory(this, MODEL_CATEGORY, 'model selection');
			},

			async getReasoningEffortOptions(this: ILoadOptionsFunctions): Promise<INodePropertyOptions[]> {
				return await configOptionsForCategory(this, REASONING_CATEGORY, 'reasoning effort selection');
			},
		},
	};

	async execute(this: IExecuteFunctions): Promise<INodeExecutionData[][]> {
		const credentials = await this.getCredentials(CRED);
		const endpoint = parseAcpEndpoint(String(credentials.baseUrl));
		const items = this.getInputData();
		const out: INodeExecutionData[] = [];

		for (let i = 0; i < items.length; i++) {
			try {
				const outputParser = await outputParserForItem(this, i);
				const tools = await connectedTools(this);
				const text = await runAcpPrompt(endpoint, {
					prompt: promptWithFormatInstructions(promptForItem(this, items[i], i), outputParser),
					cwd: optionString(this, i, 'cwd', DEFAULT_CWD),
					timeoutMs: optionNumber(this, i, 'timeoutSeconds', DEFAULT_TIMEOUT_SECONDS) * 1000,
					tools,
					config: configValuesForItem(this, i),
				});
				out.push({ json: await outputForText(text, outputParser), pairedItem: { item: i } });
			} catch (error) {
				if (!this.continueOnFail()) {
					throw error;
				}
				out.push({ json: { error: errorMessage(error) }, pairedItem: { item: i } });
			}
		}

		return [out];
	}
}

interface AcpEndpoint {
	host: string;
	port: number;
}

interface AcpPrompt {
	prompt: string;
	cwd: string;
	timeoutMs: number;
	tools: AcpTool[];
	config: AcpConfigSelection[];
}

interface JsonRpcMessage {
	jsonrpc: string;
	id?: number | string;
	method?: string;
	params?: IDataObject | null;
	result?: unknown;
	error?: { message?: string; code?: number; data?: unknown };
}

interface OutputParserLike {
	parse(text: string): Promise<unknown>;
	getFormatInstructions?(): string;
}

interface AcpTool {
	name: string;
	description?: string;
	schema?: unknown;
	invoke?: (input: unknown) => Promise<unknown>;
	call?: (input: unknown) => Promise<unknown>;
	func?: (input: unknown) => Promise<unknown>;
}

interface AcpConfigSelection {
	category: string;
	value: string;
}

interface AcpConfigOption {
	id: string;
	name: string;
	category?: string;
	type: string;
	currentValue: string;
	options: AcpConfigOptionValue[];
}

interface AcpConfigOptionValue {
	value: string;
	name: string;
	description?: string;
}

async function configOptionsForCategory(
	ctx: ILoadOptionsFunctions,
	category: string,
	label: string,
): Promise<INodePropertyOptions[]> {
	const credentials = await ctx.getCredentials(CRED);
	const endpoint = parseAcpEndpoint(String(credentials.baseUrl));
	const options = await cachedConfigOptions(endpoint);
	const option = configOptionForCategory(options, category);

	if (option === undefined) {
		return [{ name: `Harness does not support ${label}`, value: '' }];
	}

	return [
		{ name: `Harness Default (${option.currentValue})`, value: '' },
		...option.options.map((value) => ({
			name: value.name,
			value: value.value,
			description: value.description,
		})),
	];
}

function configValuesForItem(ctx: IExecuteFunctions, itemIndex: number): AcpConfigSelection[] {
	return [
		{ category: MODEL_CATEGORY, value: String(ctx.getNodeParameter('model', itemIndex, '')) },
		{ category: REASONING_CATEGORY, value: String(ctx.getNodeParameter('reasoningEffort', itemIndex, '')) },
	].filter((selection) => selection.value !== '');
}

function optionString(ctx: IExecuteFunctions, itemIndex: number, key: string, fallback: string): string {
	const options = ctx.getNodeParameter('options', itemIndex, {}) as IDataObject;
	const value = options[key] ?? ctx.getNodeParameter(key, itemIndex, fallback);
	return typeof value === 'string' && value !== '' ? value : fallback;
}

function optionNumber(ctx: IExecuteFunctions, itemIndex: number, key: string, fallback: number): number {
	const options = ctx.getNodeParameter('options', itemIndex, {}) as IDataObject;
	const value = Number(options[key] ?? ctx.getNodeParameter(key, itemIndex, fallback));
	return Number.isFinite(value) && value > 0 ? value : fallback;
}

async function outputParserForItem(
	ctx: IExecuteFunctions,
	itemIndex: number,
): Promise<OutputParserLike | undefined> {
	const hasOutputParser = Boolean(ctx.getNodeParameter('hasOutputParser', itemIndex, false));
	if (!hasOutputParser) {
		return undefined;
	}
	const outputParser = await ctx.getInputConnectionData(NodeConnectionTypes.AiOutputParser, itemIndex);
	if (!isOutputParser(outputParser)) {
		throw new Error('Connected output parser did not supply a parser');
	}
	return outputParser;
}

function isOutputParser(value: unknown): value is OutputParserLike {
	return isObject(value) && typeof value.parse === 'function';
}

async function connectedTools(ctx: IExecuteFunctions): Promise<AcpTool[]> {
	const input = await ctx.getInputConnectionData(NodeConnectionTypes.AiTool, 0);
	const tools = flattenTools(input);
	const names = new Set<string>();

	for (const tool of tools) {
		if (tool.name === '') {
			throw new Error('Connected tool did not supply a name');
		}
		if (names.has(tool.name)) {
			throw new Error(`You have multiple tools with the same name: '${tool.name}', please rename them to avoid conflicts`);
		}
		names.add(tool.name);
	}

	return tools;
}

function flattenTools(value: unknown): AcpTool[] {
	if (Array.isArray(value)) {
		return value.flatMap((item) => flattenTools(item));
	}
	if (isObject(value) && Array.isArray(value.tools)) {
		return flattenTools(value.tools);
	}
	if (isAcpTool(value)) {
		return [value];
	}
	return [];
}

function isAcpTool(value: unknown): value is AcpTool {
	return (
		isObject(value) &&
		typeof value.name === 'string' &&
		(typeof value.invoke === 'function' || typeof value.call === 'function' || typeof value.func === 'function')
	);
}

function promptForItem(ctx: IExecuteFunctions, item: INodeExecutionData, itemIndex: number): string {
	const promptType = String(ctx.getNodeParameter('promptType', itemIndex));
	if (promptType === 'auto') {
		const chatInput = item.json.chatInput;
		if (typeof chatInput !== 'string') {
			throw new Error('Expected input item to contain a string chatInput field');
		}
		return chatInput;
	}
	return String(ctx.getNodeParameter('prompt', itemIndex));
}

function promptWithFormatInstructions(prompt: string, outputParser: OutputParserLike | undefined): string {
	if (outputParser?.getFormatInstructions === undefined) {
		return prompt;
	}
	const instructions = outputParser.getFormatInstructions();
	if (instructions === '') {
		return prompt;
	}
	return `${prompt}\n\n${instructions}`;
}

async function outputForText(text: string, outputParser: OutputParserLike | undefined): Promise<IDataObject> {
	if (outputParser === undefined) {
		return { output: text };
	}
	const parsed = await outputParser.parse(text);
	return isObject(parsed) ? parsed : { output: parsed as IDataObject[string] };
}

function errorMessage(error: unknown): string {
	if (error instanceof Error) {
		return error.message;
	}
	return String(error);
}

function parseAcpEndpoint(rawUrl: string): AcpEndpoint {
	const url = new URL(rawUrl.includes('://') ? rawUrl : `tcp://${rawUrl}`);
	if (url.protocol !== 'tcp:') {
		throw new Error(`ACP endpoint must use tcp://, got ${url.protocol}`);
	}

	return {
		host: url.hostname || '127.0.0.1',
		port: url.port === '' ? DEFAULT_ACP_PORT : Number(url.port),
	};
}

async function runAcpPrompt(endpoint: AcpEndpoint, input: AcpPrompt): Promise<string> {
	const client = await sharedAcpConnection(endpoint);
	const toolset = input.tools.length > 0 ? client.addToolset(input.tools) : undefined;
	let unsubscribe: (() => void) | undefined;
	try {
		const session = await client.request('session/new', {
			cwd: input.cwd,
			mcpServers:
				toolset === undefined
					? []
					: [
							{
								type: 'acp',
								name: 'n8n-tools',
								id: toolset.id,
							},
						],
		});
		const sessionId = String(session.sessionId ?? '');
		if (sessionId === '') {
			throw new Error('ACP agent did not return a sessionId');
		}
		await applyConfigSelections(client, sessionId, configOptionsFromSession(session), input.config);

		const text: string[] = [];
		unsubscribe = client.onMessage((message) => {
			const chunk = agentTextChunk(message, sessionId);
			if (chunk !== undefined) {
				text.push(chunk);
			}
		});

		await client.request('session/prompt', {
			sessionId,
			prompt: [{ type: 'text', text: input.prompt }],
		}, input.timeoutMs);
		return text.join('');
	} finally {
		unsubscribe?.();
		if (toolset !== undefined) {
			client.removeToolset(toolset.id);
		}
	}
}

async function probeConfigOptions(endpoint: AcpEndpoint, cwd: string): Promise<AcpConfigOption[]> {
	const client = await sharedAcpConnection(endpoint);
	const session = await client.request('session/new', { cwd, mcpServers: [] }, 10_000);
	return configOptionsFromSession(session);
}

function cachedConfigOptions(endpoint: AcpEndpoint): Promise<AcpConfigOption[]> {
	const key = endpointKey(endpoint);
	const cached = configOptionsCache.get(key);
	if (cached !== undefined && cached.expiresAt > Date.now()) {
		return cached.promise;
	}

	const promise = probeConfigOptions(endpoint, DEFAULT_CWD).catch((error) => {
		configOptionsCache.delete(key);
		throw error;
	});
	configOptionsCache.set(key, { expiresAt: Date.now() + CONFIG_OPTIONS_CACHE_MS, promise });
	return promise;
}

async function sharedAcpConnection(endpoint: AcpEndpoint): Promise<AcpConnection> {
	const key = endpointKey(endpoint);
	const existing = sharedConnections.get(key);
	if (existing !== undefined) {
		return await existing;
	}

	const promise = AcpConnection.connect(endpoint, 10_000)
		.then(async (client) => {
			client.onClose(() => {
				if (sharedConnections.get(key) === promise) {
					sharedConnections.delete(key);
					configOptionsCache.delete(key);
				}
			});
			await initializeAcp(client);
			return client;
		})
		.catch((error) => {
			if (sharedConnections.get(key) === promise) {
				sharedConnections.delete(key);
				configOptionsCache.delete(key);
			}
			throw error;
		});
	sharedConnections.set(key, promise);
	return await promise;
}

function endpointKey(endpoint: AcpEndpoint): string {
	return `${endpoint.host}:${endpoint.port}`;
}

async function initializeAcp(client: AcpConnection): Promise<void> {
	await client.request('initialize', {
		protocolVersion: ACP_VERSION,
		clientCapabilities: {},
		clientInfo: {
			name: 'n8n-nodes-acp',
			version: '0.0.0',
		},
	});
}

async function applyConfigSelections(
	client: AcpConnection,
	sessionId: string,
	options: AcpConfigOption[],
	selections: AcpConfigSelection[],
): Promise<void> {
	let currentOptions = options;

	for (const selection of selections) {
		const option = configOptionForCategory(currentOptions, selection.category);
		if (option === undefined) {
			continue;
		}
		const result = await client.request('session/set_config_option', {
			sessionId,
			configId: option.id,
			value: selection.value,
		});
		currentOptions = configOptionsFromSession(result);
	}
}

function configOptionsFromSession(session: IDataObject): AcpConfigOption[] {
	return Array.isArray(session.configOptions) ? session.configOptions.flatMap(configOptionFromUnknown) : [];
}

function configOptionFromUnknown(value: unknown): AcpConfigOption[] {
	if (!isObject(value) || typeof value.id !== 'string' || typeof value.name !== 'string' || value.type !== 'select' || !Array.isArray(value.options)) {
		return [];
	}

	const options = value.options.flatMap(configOptionValueFromUnknown);
	if (options.length === 0) {
		return [];
	}

	return [
		{
			id: value.id,
			name: value.name,
			category: typeof value.category === 'string' ? value.category : undefined,
			type: 'select',
			currentValue: typeof value.currentValue === 'string' ? value.currentValue : options[0].value,
			options,
		},
	];
}

function configOptionValueFromUnknown(value: unknown): AcpConfigOptionValue[] {
	if (!isObject(value) || typeof value.value !== 'string') {
		return [];
	}
	return [
		{
			value: value.value,
			name: typeof value.name === 'string' ? value.name : value.value,
			description: typeof value.description === 'string' ? value.description : undefined,
		},
	];
}

function configOptionForCategory(options: AcpConfigOption[], category: string): AcpConfigOption | undefined {
	return options.find((option) => option.category === category);
}

interface Toolset {
	id: string;
	tools: AcpTool[];
	connections: Map<string, string>;
}

function toolDescription(tool: AcpTool): IDataObject {
	return {
		name: tool.name,
		description: tool.description ?? '',
		inputSchema: zodLikeToJsonSchema(tool.schema),
	};
}

async function callTool(tools: AcpTool[], params: IDataObject): Promise<IDataObject> {
	const name = typeof params.name === 'string' ? params.name : '';
	const tool = tools.find((candidate) => candidate.name === name);
	if (tool === undefined) {
		throw new RpcError(-32602, 'Tool not found');
	}
	const args = isObject(params.arguments) ? normalizeToolArgs(params.arguments, tool.schema) : {};

	try {
		const result = await invokeTool(tool, args);
		return formatToolResult(result);
	} catch (error) {
		return {
			isError: true,
			content: [{ type: 'text', text: errorMessage(error) }],
		};
	}
}

async function invokeTool(tool: AcpTool, args: IDataObject): Promise<unknown> {
	if (typeof tool.invoke === 'function') {
		return await tool.invoke(args);
	}
	if (typeof tool.call === 'function') {
		return await tool.call(args);
	}
	if (typeof tool.func === 'function') {
		return await tool.func(args);
	}
	throw new Error(`Tool ${tool.name} cannot be invoked`);
}

function formatToolResult(result: unknown): IDataObject {
	const text = textForToolResult(result);
	return { content: [{ type: 'text', text }] };
}

function textForToolResult(result: unknown): string {
	if (typeof result === 'string') {
		return result;
	}
	if (result === null || result === undefined || typeof result === 'number' || typeof result === 'boolean' || typeof result === 'bigint') {
		return String(result);
	}
	try {
		return JSON.stringify(result) ?? String(result);
	} catch {
		return String(result);
	}
}

function zodLikeToJsonSchema(schema: unknown): IDataObject {
	if (!isObject(schema) || !isObject(schema._def)) {
		return fallbackToolSchema();
	}

	const def = schema._def;
	const typeName = typeof def.typeName === 'string' ? def.typeName : '';
	const description = typeof schema.description === 'string' ? { description: schema.description } : {};

	switch (typeName) {
		case 'ZodObject': {
			const shapeValue = typeof def.shape === 'function' ? (def.shape as () => unknown)() : def.shape;
			const shape = isObject(shapeValue) ? shapeValue : {};
			const properties: IDataObject = {};
			const required: string[] = [];
			for (const [key, child] of Object.entries(shape)) {
				properties[key] = zodLikeToJsonSchema(child);
				if (!isOptionalZod(child)) {
					required.push(key);
				}
			}
			return { type: 'object', properties, required, additionalProperties: false, ...description };
		}
		case 'ZodString':
			return { type: 'string', ...description };
		case 'ZodNumber':
			return { type: 'number', ...description };
		case 'ZodBoolean':
			return { type: 'boolean', ...description };
		case 'ZodArray':
			return { type: 'array', items: zodLikeToJsonSchema(def.type), ...description };
		case 'ZodOptional':
		case 'ZodNullable':
		case 'ZodDefault':
			return zodLikeToJsonSchema(def.innerType);
		case 'ZodEffects':
			return zodLikeToJsonSchema(def.schema);
		case 'ZodEnum':
			return { type: 'string', enum: Array.isArray(def.values) ? def.values : [], ...description };
		case 'ZodLiteral':
			return { const: def.value, ...description };
		case 'ZodUnion':
			return { anyOf: Array.isArray(def.options) ? def.options.map(zodLikeToJsonSchema) : [], ...description };
		default:
			// ponytail: tiny Zod v3 converter; add zod-to-json-schema only if tool schemas need it.
			return fallbackToolSchema();
	}
}

function isOptionalZod(value: unknown): boolean {
	return isObject(value) && isObject(value._def) && ['ZodOptional', 'ZodDefault'].includes(String(value._def.typeName));
}

function normalizeToolArgs(args: IDataObject, schema: unknown): IDataObject {
	if (!isObject(schema) || !isObject(schema._def)) {
		return args;
	}

	const def = schema._def;
	const typeName = String(def.typeName);
	if (['ZodOptional', 'ZodNullable', 'ZodDefault'].includes(typeName)) {
		return normalizeToolArgs(args, def.innerType);
	}
	if (typeName === 'ZodEffects') {
		return normalizeToolArgs(args, def.schema);
	}
	if (typeName !== 'ZodObject') {
		return args;
	}

	const shapeValue = typeof def.shape === 'function' ? (def.shape as () => unknown)() : def.shape;
	const shape = isObject(shapeValue) ? shapeValue : {};
	const normalized: IDataObject = { ...args };
	for (const [key, child] of Object.entries(shape)) {
		if (isOptionalZod(child) && normalized[key] === '') {
			delete normalized[key];
			continue;
		}
		if (isObject(normalized[key])) {
			normalized[key] = normalizeToolArgs(normalized[key], child);
		}
	}
	return normalized;
}

function fallbackToolSchema(): IDataObject {
	return {
		type: 'object',
		properties: {
			input: { type: 'string' },
		},
		required: ['input'],
	};
}

function agentTextChunk(message: JsonRpcMessage, sessionId: string): string | undefined {
	if (message.method !== 'session/update' || message.params?.sessionId !== sessionId) {
		return undefined;
	}
	const update = message.params.update;
	if (!isObject(update) || update.sessionUpdate !== 'agent_message_chunk') {
		return undefined;
	}
	const content = update.content;
	if (!isObject(content) || content.type !== 'text') {
		return undefined;
	}
	return typeof content.text === 'string' ? content.text : undefined;
}

function isObject(value: unknown): value is IDataObject {
	return typeof value === 'object' && value !== null && !Array.isArray(value);
}

class AcpConnection {
	private readonly pending = new Map<
		number,
		{ resolve: (value: IDataObject) => void; reject: (error: Error) => void }
	>();

	private readonly listeners: Array<(message: JsonRpcMessage) => void> = [];
	private readonly closeListeners: Array<() => void> = [];
	private readonly requestHandlers = new Map<string, (params: IDataObject) => Promise<unknown> | unknown>();
	private readonly toolsets = new Map<string, Toolset>();
	private buffer = '';
	private nextId = 1;
	private closed = false;

	private constructor(
		private readonly socket: net.Socket,
		private readonly timeoutMs: number,
	) {
		socket.on('data', (chunk) => this.read(chunk));
		socket.on('error', (error) => this.failAll(error));
		socket.on('close', () => {
			this.closed = true;
			this.failAll(new Error('ACP connection closed'));
			for (const listener of this.closeListeners) {
				listener();
			}
		});
		this.installMcpHandlers();
	}

	static connect(endpoint: AcpEndpoint, timeoutMs: number): Promise<AcpConnection> {
		return new Promise((resolve, reject) => {
			const socket = net.createConnection(endpoint.port, endpoint.host);
			const timer = setTimeout(() => {
				socket.destroy();
				reject(new Error('ACP connection timed out'));
			}, timeoutMs);

			socket.once('connect', () => {
				clearTimeout(timer);
				resolve(new AcpConnection(socket, timeoutMs));
			});
			socket.once('error', (error) => {
				clearTimeout(timer);
				reject(error);
			});
		});
	}

	request(method: string, params: IDataObject, timeoutMs = this.timeoutMs): Promise<IDataObject> {
		const id = this.nextId++;
		const message = { jsonrpc: JSON_RPC_VERSION, id, method, params };

		return new Promise((resolve, reject) => {
			if (this.closed) {
				reject(new Error('ACP connection closed'));
				return;
			}
			const timer = setTimeout(() => {
				this.pending.delete(id);
				reject(new Error(`ACP request timed out: ${method}`));
			}, timeoutMs);

			this.pending.set(id, {
				resolve: (value) => {
					clearTimeout(timer);
					resolve(value);
				},
				reject: (error) => {
					clearTimeout(timer);
					reject(error);
				},
			});
			this.socket.write(`${JSON.stringify(message)}\n`);
		});
	}

	onMessage(listener: (message: JsonRpcMessage) => void): () => void {
		this.listeners.push(listener);
		return () => {
			const index = this.listeners.indexOf(listener);
			if (index !== -1) {
				this.listeners.splice(index, 1);
			}
		};
	}

	onRequest(method: string, handler: (params: IDataObject) => Promise<unknown> | unknown): void {
		this.requestHandlers.set(method, handler);
	}

	onClose(listener: () => void): void {
		this.closeListeners.push(listener);
	}

	addToolset(tools: AcpTool[]): Toolset {
		const toolset = { id: randomUUID(), tools, connections: new Map<string, string>() };
		this.toolsets.set(toolset.id, toolset);
		return toolset;
	}

	removeToolset(id: string): void {
		this.toolsets.delete(id);
	}

	close(): void {
		this.closed = true;
		this.socket.end();
	}

	private installMcpHandlers(): void {
		this.onRequest('mcp/connect', (params) => {
			const acpID = typeof params.acpId === 'string' ? params.acpId : '';
			const toolset = this.toolsets.get(acpID);
			if (toolset === undefined) {
				throw new RpcError(-32602, 'Unknown MCP server id');
			}
			const connectionId = randomUUID();
			toolset.connections.set(connectionId, toolset.id);
			return { connectionId };
		});

		this.onRequest('mcp/message', async (params) => {
			const connectionId = typeof params.connectionId === 'string' ? params.connectionId : '';
			const toolset = this.toolsetForConnection(connectionId);
			if (toolset === undefined) {
				throw new RpcError(-32602, 'Unknown MCP connection id');
			}
			const method = typeof params.method === 'string' ? params.method : '';
			const innerParams = isObject(params.params) ? params.params : {};

			if (method === 'tools/list') {
				return { tools: toolset.tools.map(toolDescription) };
			}
			if (method === 'tools/call') {
				return await callTool(toolset.tools, innerParams);
			}
			throw new RpcError(-32601, `Unknown MCP method: ${method}`);
		});

		this.onRequest('mcp/disconnect', (params) => {
			const connectionId = typeof params.connectionId === 'string' ? params.connectionId : '';
			this.toolsetForConnection(connectionId)?.connections.delete(connectionId);
			return {};
		});
	}

	private toolsetForConnection(connectionId: string): Toolset | undefined {
		for (const toolset of this.toolsets.values()) {
			if (toolset.connections.has(connectionId)) {
				return toolset;
			}
		}
		return undefined;
	}

	private read(chunk: Buffer): void {
		this.buffer += chunk.toString('utf8');
		for (;;) {
			const lineEnd = this.buffer.indexOf('\n');
			if (lineEnd === -1) {
				return;
			}

			const raw = this.buffer.slice(0, lineEnd).trim();
			this.buffer = this.buffer.slice(lineEnd + 1);
			if (raw !== '') {
				if (!raw.startsWith('{')) {
					// ponytail: OpenCode can leak text logs onto stdio; ACP frames are JSON objects.
					continue;
				}
				try {
					this.dispatch(JSON.parse(raw) as JsonRpcMessage);
				} catch (error) {
					this.failAll(error instanceof Error ? error : new Error(String(error)));
					this.close();
					return;
				}
			}
		}
	}

	private dispatch(message: JsonRpcMessage): void {
		if (typeof message.id === 'number') {
			const pending = this.pending.get(message.id);
			if (pending !== undefined) {
				this.pending.delete(message.id);
				if (message.error !== undefined) {
					pending.reject(new Error(message.error.message ?? `ACP error ${message.error.code ?? ''}`));
				} else {
					pending.resolve(isObject(message.result) ? message.result : {});
				}
				return;
			}
		}

		if (message.id !== undefined && message.method !== undefined) {
			void this.handleRequest(message);
			return;
		}

		for (const listener of this.listeners) {
			listener(message);
		}
	}

	private async handleRequest(message: JsonRpcMessage): Promise<void> {
		const id = message.id;
		const method = message.method ?? '';
		const handler = this.requestHandlers.get(method);
		if (handler === undefined) {
			this.respondError(id, -32601, `Unknown ACP method: ${method}`);
			return;
		}

		try {
			const params = isObject(message.params) ? message.params : {};
			this.respond(id, await handler(params));
		} catch (error) {
			if (error instanceof RpcError) {
				this.respondError(id, error.code, error.message);
			} else {
				this.respondError(id, -32000, errorMessage(error));
			}
		}
	}

	private respond(id: number | string | undefined, result: unknown): void {
		if (id === undefined) {
			return;
		}
		this.socket.write(`${JSON.stringify({ jsonrpc: JSON_RPC_VERSION, id, result })}\n`);
	}

	private respondError(id: number | string | undefined, code: number, message: string): void {
		if (id === undefined) {
			return;
		}
		this.socket.write(`${JSON.stringify({ jsonrpc: JSON_RPC_VERSION, id, error: { code, message } })}\n`);
	}

	private failAll(error: Error): void {
		for (const pending of this.pending.values()) {
			pending.reject(error);
		}
		this.pending.clear();
	}
}

class RpcError extends Error {
	constructor(
		readonly code: number,
		message: string,
	) {
		super(message);
	}
}
