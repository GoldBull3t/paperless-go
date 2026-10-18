function s(t,e){for(let i=1,n=1;i<t.length;i++,n++)if(e[n]==="\\")n++;else if(t[i]===":")return i;throw new Error(`Unterminated $localize metadata block in "${e}".`)}var o=function(t,...e){if(o.translate){let n=o.translate(t,e);t=n[0],e=n[1]}let i=r(t[0],t.raw[0]);for(let n=1;n<t.length;n++)i+=e[n-1]+r(t[n],t.raw[n]);return i},l=":";function r(t,e){return e.charAt(0)===l?t.substring(s(t,e)+1):t}globalThis.$localize=o;(globalThis.$localize??={}).locale="id-ID";
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

return 5;
}
    global.ng.common.locales['id'] = ["id",[["AM","PM"]],u,[["M","S","S","R","K","J","S"],["Min","Sen","Sel","Rab","Kam","Jum","Sab"],["Minggu","Senin","Selasa","Rabu","Kamis","Jumat","Sabtu"],["Min","Sen","Sel","Rab","Kam","Jum","Sab"]],u,[["J","F","M","A","M","J","J","A","S","O","N","D"],["Jan","Feb","Mar","Apr","Mei","Jun","Jul","Agu","Sep","Okt","Nov","Des"],["Januari","Februari","Maret","April","Mei","Juni","Juli","Agustus","September","Oktober","November","Desember"]],u,[["SM","M"],u,["Sebelum Masehi","Masehi"]],0,[6,0],["dd/MM/yy","d MMM y","d MMMM y","EEEE, dd MMMM y"],["HH.mm","HH.mm.ss","HH.mm.ss z","HH.mm.ss zzzz"],["{1} {0}",u,u,u],[",",".",";","%","+","-","E","×","‰","∞","NaN","."],["#,##0.###","#,##0%","¤#,##0.00","#E0"],"IDR","Rp","Rupiah Indonesia",{"AUD":["AU$","$"],"BYN":[u,"р."],"IDR":["Rp"],"INR":["Rs","₹"],"JPY":["JP¥","¥"],"PHP":[u,"₱"],"THB":["฿"],"TWD":["NT$"],"USD":["US$","$"],"XXX":[]},"ltr", plural, [[["tengah malam","tengah hari","pagi","siang","sore","malam"],u,u],u,["00:00","12:00",["00:00","10:00"],["10:00","15:00"],["15:00","18:00"],["18:00","24:00"]]]];
  })(globalThis);
    
;;
/**i18n:fa3d7e19b336ec75*/
