function s(t,e){for(let i=1,n=1;i<t.length;i++,n++)if(e[n]==="\\")n++;else if(t[i]===":")return i;throw new Error(`Unterminated $localize metadata block in "${e}".`)}var o=function(t,...e){if(o.translate){let n=o.translate(t,e);t=n[0],e=n[1]}let i=r(t[0],t.raw[0]);for(let n=1;n<t.length;n++)i+=e[n-1]+r(t[n],t.raw[n]);return i},l=":";function r(t,e){return e.charAt(0)===l?t.substring(s(t,e)+1):t}globalThis.$localize=o;(globalThis.$localize??={}).locale="af-ZA";
/**
 * @license
 * Copyright Google LLC All Rights Reserved.
 *
 * Use of this source code is governed by an MIT-style license that can be
 * found in the LICENSE file at https://angular.dev/license
 */

// THIS CODE IS GENERATED - DO NOT MODIFY.
  (function(global) {
    global.ng ??= {};
    global.ng.common ??= {};
    global.ng.common.locales ??= {};
    const u = undefined;
    function plural(val) {
const n = val;

if (n === 1)
    return 1;
return 5;
}
    global.ng.common.locales['af'] = ["af",[["v","n"],["vm.","nm."]],u,[["S","M","D","W","D","V","S"],["So.","Ma.","Di.","Wo.","Do.","Vr.","Sa."],["Sondag","Maandag","Dinsdag","Woensdag","Donderdag","Vrydag","Saterdag"],["So.","Ma.","Di.","Wo.","Do.","Vr.","Sa."]],u,[["J","F","M","A","M","J","J","A","S","O","N","D"],["Jan.","Feb.","Mrt.","Apr.","Mei","Jun.","Jul.","Aug.","Sep.","Okt.","Nov.","Des."],["Januarie","Februarie","Maart","April","Mei","Junie","Julie","Augustus","September","Oktober","November","Desember"]],u,[["v.C.","n.C."],u,["voor Christus","ná Christus"]],0,[6,0],["y-MM-dd","dd MMM y","dd MMMM y","EEEE dd MMMM y"],["HH:mm","HH:mm:ss","HH:mm:ss z","HH:mm:ss zzzz"],["{1} {0}",u,u,u],[","," ",";","%","+","-","E","×","‰","∞","NaN",":"],["#,##0.###","#,##0%","¤#,##0.00","#E0"],"ZAR","R","Suid-Afrikaanse rand",{"BYN":[u,"р."],"CAD":[u,"$"],"JPY":["JP¥","¥"],"MXN":[u,"$"],"PHP":[u,"₱"],"RON":[u,"leu"],"THB":["฿"],"TWD":["NT$"],"USD":[u,"$"],"ZAR":["R"]},"ltr", plural, [[["mn","o","m","a","n"],["middernag","die oggend","die middag","die aand","die nag"],u],[["mn","o","m","a","n"],["middernag","oggend","middag","aand","nag"],u],["00:00",["05:00","12:00"],["12:00","18:00"],["18:00","24:00"],["00:00","05:00"]]]];
  })(globalThis);
    
;;
/**i18n:fa3d7e19b336ec75*/
