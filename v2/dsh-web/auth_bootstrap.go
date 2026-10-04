package dshweb

import "bytes"

const authenticationBootstrap = `<script>
(()=>{
const gate=globalThis.__DSH_BOOT_READY__??=Promise.withResolvers();
const key="agentserver-v2.dsh-auth.v1",transactionKey="agentserver-v2.dsh-pkce.v1";
const b64=(bytes)=>{let s="";for(const b of bytes)s+=String.fromCharCode(b);return btoa(s).replace(/\+/g,"-").replace(/\//g,"_").replace(/=+$/g,"")};
const random=()=>{const bytes=new Uint8Array(32);crypto.getRandomValues(bytes);return b64(bytes)};
const json=async(response)=>{if(!response.ok)throw new Error("DSH authentication request failed: HTTP "+response.status);return response.json()};
const stored=()=>{try{const value=JSON.parse(localStorage.getItem(key)||"null");return value&&typeof value.accessToken==="string"&&value.accessToken&&typeof value.expiresAt==="number"&&value.expiresAt>Date.now()+30000?value:undefined}catch(_){return undefined}};
const install=async(token)=>{const original=globalThis.fetch.bind(globalThis);globalThis.fetch=(input,init)=>{const options=init?{...init}:{};const headers=new Headers(options.headers||{});if(!headers.has("Authorization"))headers.set("Authorization","Bearer "+token);options.headers=headers;options.credentials=options.credentials||"include";return original(input,options)};const response=await globalThis.fetch("/api/session/list",{method:"POST",headers:{"content-type":"application/json"},body:JSON.stringify({type:"client-request",rpcId:"dsh-auth-bootstrap",method:"session/list",payload:{args:{}}})});if(!response.ok)throw new Error("DSH bearer bootstrap failed: HTTP "+response.status)};
const start=async()=>{try{const config=await json(await fetch("/auth/config",{cache:"no-store"}));const workspace=await json(await fetch("/auth/dsh/config",{cache:"no-store"}));let token=stored();const callback=new URLSearchParams(location.search);if(callback.has("code")||callback.has("error")){const transaction=JSON.parse(sessionStorage.getItem(transactionKey)||"null");sessionStorage.removeItem(transactionKey);if(!transaction||transaction.state!==callback.get("state"))throw new Error("DSH OAuth state is invalid");if(callback.get("error"))throw new Error("Authorization failed: "+callback.get("error"));const response=await fetch(config.tokenEndpoint,{method:"POST",headers:{"content-type":"application/x-www-form-urlencoded"},body:new URLSearchParams({grant_type:"authorization_code",code:callback.get("code")||"",redirect_uri:transaction.redirectUri,client_id:config.clientId,code_verifier:transaction.verifier}),credentials:"include"});const value=await json(response);if(typeof value.access_token!=="string"||typeof value.expires_in!=="number")throw new Error("OAuth token response is invalid");token={accessToken:value.access_token,expiresAt:Date.now()+value.expires_in*1000,workspaceId:workspace.workspaceId};localStorage.setItem(key,JSON.stringify(token));history.replaceState(null,"",location.pathname)}if(!token){const verifier=random(),state=random(),digest=await crypto.subtle.digest("SHA-256",new TextEncoder().encode(verifier)),challenge=b64(new Uint8Array(digest)),redirectUri=location.origin+"/";sessionStorage.setItem(transactionKey,JSON.stringify({state,verifier,redirectUri}));const url=new URL(config.authorizationEndpoint);url.search=new URLSearchParams({response_type:"code",client_id:config.clientId,redirect_uri:redirectUri,scope:config.scopes.join(" "),audience:config.audience,state,code_challenge:challenge,code_challenge_method:"S256",resource:"urn:agentserver:workspace:"+workspace.workspaceId}).toString();location.assign(url.href);return}await install(token.accessToken);gate.resolve()}catch(error){gate.reject(error)}};
globalThis.__DSH_AUTH_READY__=start;
})()
</script>`

func withAuthenticationBootstrap(index []byte) []byte {
	ready := []byte(`<script>(globalThis.__DSH_BOOT_READY__ ??= Promise.withResolvers()).resolve()</script>`)
	deferred := []byte(`<script>(globalThis.__DSH_AUTH_READY__ ?? (()=>{(globalThis.__DSH_BOOT_READY__ ??= Promise.withResolvers()).resolve()}))()</script>`)
	index = bytes.Replace(index, ready, deferred, 1)
	return bytes.Replace(index, []byte(`</head>`), []byte(authenticationBootstrap+`</head>`), 1)
}
