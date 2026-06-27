import type {
	IDataObject,
	IExecuteFunctions,
	INodeExecutionData,
	INodeType,
	INodeTypeDescription,
} from 'n8n-workflow';
import { NodeConnectionTypes } from 'n8n-workflow';
import net from 'node:net';

const CRED = 'acpAgentApi';
const DEFAULT_ACP_PORT = 8080;
const JSON_RPC_VERSION = '2.0';
const ACP_VERSION = 1;

export class AcpAgent implements INodeType {
	description: INodeTypeDescription = {
		displayName: 'ACP Agent',
		name: 'acpAgent',
		group: ['transform'],
		version: 1,
		description: 'Run workflow data through an ACP-enabled agent harness',
		defaults: { name: 'ACP Agent' },
		inputs: [NodeConnectionTypes.Main],
		outputs: [NodeConnectionTypes.Main],
		credentials: [{ name: CRED, required: true }],
		properties: [
			{
				displayName: 'Prompt',
				name: 'prompt',
				type: 'string',
				typeOptions: { rows: 8 },
				default: '',
				required: true,
				description: 'Prompt sent to the ACP runner with each input item',
			},
			{
				displayName: 'Working Directory',
				name: 'cwd',
				type: 'string',
				default: '/workspace',
				required: true,
				description: 'Absolute working directory for the ACP session',
			},
			{
				displayName: 'Timeout Seconds',
				name: 'timeoutSeconds',
				type: 'number',
				typeOptions: { minValue: 1 },
				default: 120,
				description: 'Maximum time the runner should spend on one item',
			},
		],
	};

	async execute(this: IExecuteFunctions): Promise<INodeExecutionData[][]> {
		const credentials = await this.getCredentials(CRED);
		const endpoint = parseAcpEndpoint(String(credentials.baseUrl));
		const items = this.getInputData();
		const out: INodeExecutionData[] = [];

		for (let i = 0; i < items.length; i++) {
			const text = await runAcpPrompt(endpoint, {
				prompt: String(this.getNodeParameter('prompt', i)),
				cwd: String(this.getNodeParameter('cwd', i)),
				timeoutMs: Number(this.getNodeParameter('timeoutSeconds', i)) * 1000,
			});
			out.push({ json: { output: text }, pairedItem: { item: i } });
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
}

interface JsonRpcMessage {
	jsonrpc: string;
	id?: number;
	method?: string;
	params?: IDataObject;
	result?: IDataObject;
	error?: { message?: string; code?: number; data?: unknown };
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
	const client = await AcpConnection.connect(endpoint, input.timeoutMs);
	try {
		await client.request('initialize', {
			protocolVersion: ACP_VERSION,
			clientCapabilities: {},
			clientInfo: {
				name: 'n8n-nodes-acp',
				version: '0.0.0',
			},
		});
		const session = await client.request('session/new', {
			cwd: input.cwd,
			mcpServers: [],
		});
		const sessionId = String(session.sessionId ?? '');
		if (sessionId === '') {
			throw new Error('ACP agent did not return a sessionId');
		}

		const text: string[] = [];
		client.onMessage((message) => {
			const chunk = agentTextChunk(message, sessionId);
			if (chunk !== undefined) {
				text.push(chunk);
			}
		});

		await client.request('session/prompt', {
			sessionId,
			prompt: [{ type: 'text', text: input.prompt }],
		});
		return text.join('');
	} finally {
		client.close();
	}
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
	private buffer = '';
	private nextId = 1;
	private closed = false;

	private constructor(
		private readonly socket: net.Socket,
		private readonly timeoutMs: number,
	) {
		socket.on('data', (chunk) => this.read(chunk));
		socket.on('error', (error) => this.failAll(error));
		socket.on('close', () => this.failAll(new Error('ACP connection closed')));
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

	request(method: string, params: IDataObject): Promise<IDataObject> {
		const id = this.nextId++;
		const message = { jsonrpc: JSON_RPC_VERSION, id, method, params };

		return new Promise((resolve, reject) => {
			const timer = setTimeout(() => {
				this.pending.delete(id);
				reject(new Error(`ACP request timed out: ${method}`));
			}, this.timeoutMs);

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

	onMessage(listener: (message: JsonRpcMessage) => void): void {
		this.listeners.push(listener);
	}

	close(): void {
		this.closed = true;
		this.socket.end();
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
		if (message.id !== undefined) {
			const pending = this.pending.get(message.id);
			if (pending !== undefined) {
				this.pending.delete(message.id);
				if (message.error !== undefined) {
					pending.reject(new Error(message.error.message ?? `ACP error ${message.error.code ?? ''}`));
				} else {
					pending.resolve(message.result ?? {});
				}
				return;
			}
		}

		for (const listener of this.listeners) {
			listener(message);
		}
	}

	private failAll(error: Error): void {
		if (this.closed) {
			return;
		}
		for (const pending of this.pending.values()) {
			pending.reject(error);
		}
		this.pending.clear();
	}
}
