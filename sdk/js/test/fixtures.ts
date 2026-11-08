import type { Signer } from '../src/index.js';

// One body that satisfies every login flow: the challenge step reads
// "challenge", the login step reads its token.
export const LOGIN_REPLY = '{"challenge":"c1","session_token":"s1","access_token":"a1","api_key":"k1"}';

export const S: Signer = {
  publicKey: 'ssh-ed25519 AAAAC3Nza test',
  sign: (data) => ({ format: 'ssh-ed25519', signature: new Uint8Array([...new TextEncoder().encode('sig:'), ...data]) }),
};
