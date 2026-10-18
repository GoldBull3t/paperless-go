function s(t,e){for(let i=1,n=1;i<t.length;i++,n++)if(e[n]==="\\")n++;else if(t[i]===":")return i;throw new Error(`Unterminated $localize metadata block in "${e}".`)}var o=function(t,...e){if(o.translate){let n=o.translate(t,e);t=n[0],e=n[1]}let i=r(t[0],t.raw[0]);for(let n=1;n<t.length;n++)i+=e[n-1]+r(t[n],t.raw[n]);return i},l=":";function r(t,e){return e.charAt(0)===l?t.substring(s(t,e)+1):t}globalThis.$localize=o;(globalThis.$localize??={}).locale="el-GR";
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
    global.ng.common.locales['el'] = ["el",[["πμ","μμ"],["π.μ.","μ.μ."]],u,[["Κ","Δ","Τ","Τ","Π","Π","Σ"],["Κυρ","Δευ","Τρί","Τετ","Πέμ","Παρ","Σάβ"],["Κυριακή","Δευτέρα","Τρίτη","Τετάρτη","Πέμπτη","Παρασκευή","Σάββατο"],["Κυ","Δε","Τρ","Τε","Πέ","Πα","Σά"]],u,[["Ι","Φ","Μ","Α","Μ","Ι","Ι","Α","Σ","Ο","Ν","Δ"],["Ιαν","Φεβ","Μαρ","Απρ","Μαΐ","Ιουν","Ιουλ","Αυγ","Σεπ","Οκτ","Νοε","Δεκ"],["Ιανουαρίου","Φεβρουαρίου","Μαρτίου","Απριλίου","Μαΐου","Ιουνίου","Ιουλίου","Αυγούστου","Σεπτεμβρίου","Οκτωβρίου","Νοεμβρίου","Δεκεμβρίου"]],[["Ι","Φ","Μ","Α","Μ","Ι","Ι","Α","Σ","Ο","Ν","Δ"],["Ιαν","Φεβ","Μάρ","Απρ","Μάι","Ιούν","Ιούλ","Αύγ","Σεπ","Οκτ","Νοέ","Δεκ"],["Ιανουάριος","Φεβρουάριος","Μάρτιος","Απρίλιος","Μάιος","Ιούνιος","Ιούλιος","Αύγουστος","Σεπτέμβριος","Οκτώβριος","Νοέμβριος","Δεκέμβριος"]],[["π.Χ.","μ.Χ."],u,["προ Χριστού","μετά Χριστόν"]],1,[6,0],["d/M/yy","d MMM y","d MMMM y","EEEE d MMMM y"],["h:mm a","h:mm:ss a","h:mm:ss a z","h:mm:ss a zzzz"],["{1}, {0}",u,"{1} - {0}",u],[",",".",";","%","+","-","e","×","‰","∞","NaN",":"],["#,##0.###","#,##0%","#,##0.00 ¤","#E0"],"EUR","€","Ευρώ",{"BYN":[u,"р."],"GRD":["Δρχ"],"JPY":["JP¥","¥"],"PHP":[u,"₱"],"THB":["฿"]},"ltr", plural, [[["πρωί","μεσημ.","απόγ.","βράδυ"],u,["το πρωί","το μεσημέρι","το απόγευμα","το βράδυ"]],[["πρωί","μεσημ.","απόγ.","βράδυ"],u,["πρωί","μεσημέρι","απόγευμα","βράδυ"]],[["04:00","12:00"],["12:00","17:00"],["17:00","20:00"],["20:00","04:00"]]]];
  })(globalThis);
    
;;
/**i18n:fa3d7e19b336ec75*/
