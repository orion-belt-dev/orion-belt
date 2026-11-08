import { createServer, type IncomingHttpHeaders, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';

export type Recorded = {
  method: string;
  /** Path plus query, as sent. */
  target: string;
  /** Node lowercases header names. */
  headers: IncomingHttpHeaders;
  body: Buffer;
};

/** Answers every request with one canned reply and keeps what it received. */
export class RecordingServer {
  url = '';
  status = 200;
  body: Buffer = Buffer.from('{}');
  contentType = 'application/json';
  requests: Recorded[] = [];
  /** Per-path replies, for flows that make more than one call. */
  routes = new Map<string, string>();
  private server: Server | undefined;

  async start(): Promise<void> {
    this.server = createServer((req, res) => {
      const chunks: Buffer[] = [];
      req.on('data', (c: Buffer) => chunks.push(c));
      req.on('end', () => {
        const target = req.url ?? '';
        this.requests.push({ method: req.method ?? '', target, headers: req.headers, body: Buffer.concat(chunks) });
        const route = this.routes.get(target.split('?')[0] ?? '');
        const reply = route === undefined ? this.body : Buffer.from(route);
        res.writeHead(this.status, { 'Content-Type': this.contentType, 'Content-Length': reply.length });
        res.end(reply);
      });
    });
    await new Promise<void>((resolve) => this.server?.listen(0, '127.0.0.1', resolve));
    this.url = `http://127.0.0.1:${(this.server.address() as AddressInfo).port}`;
  }

  async stop(): Promise<void> {
    this.server?.closeAllConnections();
    await new Promise<void>((resolve) => this.server?.close(() => resolve()));
  }

  reply(status: number, body: string | Buffer, contentType = 'application/json'): void {
    this.status = status;
    this.body = typeof body === 'string' ? Buffer.from(body) : body;
    this.contentType = contentType;
  }

  reset(): void {
    this.requests = [];
    this.routes.clear();
    this.reply(200, '{}');
  }

  get last(): Recorded {
    const last = this.requests.at(-1);
    if (!last) {
      throw new Error('no request recorded');
    }
    return last;
  }
}
