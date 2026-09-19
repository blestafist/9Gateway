import{c as s,r as f,j as p}from"./index-C1eqmOHN.js";/**
 * @license lucide-react v1.47.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const v={name:"activity",size:24,node:[["path",{d:"M22 12h-2.48a2 2 0 0 0-1.93 1.46l-2.35 8.36a.25.25 0 0 1-.48 0L9.24 2.18a.25.25 0 0 0-.48 0l-2.35 8.36A2 2 0 0 1 4.49 12H2",key:"169zse"}]]};v.node;const L=s(v);/**
 * @license lucide-react v1.47.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const g={name:"arrow-right",size:24,node:[["path",{d:"M5 12h14",key:"1ays0h"}],["path",{d:"m12 5 7 7-7 7",key:"xquz4c"}]]};g.node;const j=s(g);/**
 * @license lucide-react v1.47.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const k={name:"log-in",size:24,node:[["path",{d:"m10 17 5-5-5-5",key:"1bsop3"}],["path",{d:"M15 12H3",key:"6jk70r"}],["path",{d:"M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4",key:"u53s6r"}]]};k.node;const A=s(k);/**
 * @license lucide-react v1.47.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const w={name:"minus",size:24,node:[["path",{d:"M5 12h14",key:"1ays0h"}]]};w.node;const R=s(w);/**
 * @license lucide-react v1.47.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const x={name:"refresh-cw",size:24,node:[["path",{d:"M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8",key:"v9h5vc"}],["path",{d:"M21 3v5h-5",key:"1q7to0"}],["path",{d:"M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16",key:"3uifl3"}],["path",{d:"M8 16H3v5",key:"1cv678"}]]};x.node;const H=s(x);/**
 * @license lucide-react v1.47.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const $={name:"trending-down",size:24,node:[["path",{d:"M16 17h6v-6",key:"t6n2it"}],["path",{d:"m22 17-8.5-8.5-5 5L2 7",key:"x473p"}]]};$.node;const N=s($);/**
 * @license lucide-react v1.47.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const D={name:"trending-up",size:24,node:[["path",{d:"M16 7h6v6",key:"box55l"}],["path",{d:"m22 7-8.5 8.5-5-5L2 17",key:"1t1m79"}]]};D.node;const E=s(D),T=({items:l,activeTab:y,onChange:u,variant:I="segmented","aria-label":M,className:_=""})=>{const h=f.useId(),b=f.useRef(new Map),t=l.some(e=>e.content!==void 0&&e.content!==null),d=l.filter(e=>!e.disabled),m=(e,o)=>{var a;const c=d.findIndex(r=>r.id===o);if(c===-1)return;let n=-1;if(e.key==="ArrowRight"?(e.preventDefault(),n=(c+1)%d.length):e.key==="ArrowLeft"?(e.preventDefault(),n=(c-1+d.length)%d.length):e.key==="Home"?(e.preventDefault(),n=0):e.key==="End"&&(e.preventDefault(),n=d.length-1),n!==-1){const r=d[n];r&&(u(r.id),(a=b.current.get(r.id))==null||a.focus())}},i=l.find(e=>e.id===y);return p.jsxs("div",{className:`gw-tabs-container ${_}`,children:[p.jsx("div",{role:t?"tablist":"group","aria-label":M,className:`gw-tablist gw-tablist--${I}`,children:l.map(e=>{const o=e.id===y,c=`${h}-tab-${e.id}`,n=`${h}-panel-${e.id}`;return p.jsx("button",{ref:a=>{a?b.current.set(e.id,a):b.current.delete(e.id)},id:t?c:void 0,role:t?"tab":void 0,type:"button","aria-selected":t?o:void 0,"aria-pressed":t?void 0:o,"aria-controls":t&&e.content&&o?n:void 0,tabIndex:o?0:-1,disabled:e.disabled,className:"gw-tab",onClick:()=>!e.disabled&&u(e.id),onKeyDown:a=>m(a,e.id),children:e.label},e.id)})}),t&&i&&i.content&&p.jsx("div",{id:`${h}-panel-${i.id}`,role:"tabpanel","aria-labelledby":`${h}-tab-${i.id}`,tabIndex:0,className:"gw-tabpanel",children:i.content})]})};export{j as A,A as L,R as M,H as R,E as T,N as a,L as b,T as c};
